package ipfs

import (
	"bytes"
	"errors"
	client "github.com/ipfs/go-ipfs-api"
	ipld "github.com/ipfs/go-ipld-format"
	"github.com/ipfs/go-merkledag"
	"github.com/ipfs/go-unixfs"
	unixfs_pb "github.com/ipfs/go-unixfs/pb"
	mh "github.com/multiformats/go-multihash"
	"io"
)

//func init() {
//	s, err := NewStore("http://127.0.0.1:5001", "http://127.0.0.1:8080")
//	if err != nil {
//		panic(err)
//	}
//	Gstore = s
//}

var (
	ErrLoadNode = errors.New("无法加载节点数据")
)

type Store interface {
	// node,err 从reader对象中读取创建node
	AddFromReader(io.Reader) (File, error)
	// node,err 从字节数组中读取创建node
	AddFromBytes(bytes []byte) (File, error)
	// 固定文件，长期保存，为了更好的存储必须是block
	Pin(blk Block) error
	PinMany(blks []Block) error
	// 取消固定文件
	Unpin(blk Block) error
	UnpinMany(blks []Block) error
	// 获取node
	Get(cid string) Node
	GetMany(cids []string) []Node
	// node 按照顺序组合文件块
	Combine([]Block) (File, error)
}

func NewStore(apiAddr string, gatewayAddr string) (Store, error) {
	api := client.NewShell(apiAddr)
	_, _, err := api.Version()
	if err != nil {
		return nil, err
	}
	return &store{
		api:     api,
		gateway: gatewayAddr,
	}, nil
}

type store struct {
	api     *client.Shell
	gateway string
}

func (s *store) getGateway() string {
	return s.gateway
}

func (s *store) AddFromReader(reader io.Reader) (File, error) {
	cid, err := s.api.Add(reader)
	if err != nil {
		return nil, err
	}
	node := s.get(newLink(cid))
	file, err := node.ToFile()
	if err != nil {
		return nil, err
	}
	return file, nil
}

func (s *store) AddFromBytes(data []byte) (File, error) {
	return s.AddFromReader(bytes.NewReader(data))
}

// TODO: 支持file等文件等递归操作
func (s *store) Pin(blk Block) error {
	if blk.Type() != BLK {
		return errors.New("not a block")
	}
	return s.api.Pin(blk.Cid())
}

func (s *store) PinMany(blks []Block) error {
	var err error
	for _, blk := range blks {
		err = s.Pin(blk)
		if err != nil {
			return err
		}
	}
	return err
}

func (s *store) Unpin(blk Block) error {
	if blk.Type() != BLK {
		return errors.New("unpin error:not a block")
	}
	return s.api.Unpin(blk.Cid())
}

func (s *store) UnpinMany(blks []Block) error {
	var err error
	for _, blk := range blks {
		err = s.Unpin(blk)
		if err != nil {
			return err
		}
	}
	return err
}

func (s *store) getProtoNode(link *ipld.Link) (*merkledag.ProtoNode, error) {
	cid := link.Cid.String()
	data, err := s.api.BlockGet(cid)
	if err != nil {
		return nil, err
	}
	return merkledag.DecodeProtobuf(data)
}

func (s *store) Get(cid string) Node {
	return s.get(newLink(cid))
}

// TODO:数据加载超时，运行ctx控制
func (s *store) get(link *ipld.Link) *node {
	node := newNode(link, s)
	// 加载失败
	if !node.load() {
		return node
	}
	return node
}

// TODO: 多线程处理
func (s *store) GetMany(cids []string) []Node {
	var nodes = make([]Node, 0, len(cids))
	for _, cid := range cids {
		node := s.Get(cid)
		nodes = append(nodes, node)
	}
	return nodes
}

func (s *store) getMany(links []*ipld.Link) []Node {
	var nodes = make([]Node, 0, len(links))
	for _, link := range links {
		node := s.get(link)
		nodes = append(nodes, node)
	}
	return nodes
}

func (s *store) Combine(blocks []Block) (File, error) {
	nodes := make([]Node, 0, len(blocks))
	for _, b := range blocks {
		nodes = append(nodes, b.(*block).Node)
	}
	node, err := s.combine(FIL, nodes)
	if err != nil {
		return nil, err
	}
	return node.ToFile()
}

// TODO:不直接存放数据进入ipfs，而是让用户选择是否存放
func (s *store) combine(tp Type, nodes []Node) (*node, error) {
	// 创建空的文件节点
	head := unixfs.NewFSNode(unixfs_pb.Data_DataType(tp))
	newNode := merkledag.NodeWithData(nil)
	// 添加block
	for _, node := range nodes {
		newNode.AddRawLink("", newFullLink(node.Cid(), "", node.RawSize()))
		head.AddBlockSize(node.Size())
	}
	// 生成头部pb数据，准备block
	data, err := head.GetBytes()
	if err != nil {
		return nil, err
	}
	newNode.SetData(data)
	// 存放block信息到ipfs
	cid, err := s.api.BlockPut(newNode.RawData(), "", mh.Codes[mh.SHA2_256], -1)
	if err != nil {
		panic(err)
	}
	// 从ipfs中加载存入的信息
	node := s.get(newLink(cid))
	return node, nil
}
