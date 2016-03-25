// Copyright 2015 Factom Foundation
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

package state

import (
	"fmt"
	"github.com/FactomProject/factomd/anchor"
	"github.com/FactomProject/factomd/common/constants"
	"github.com/FactomProject/factomd/common/interfaces"
	"github.com/FactomProject/factomd/common/messages"
	"github.com/FactomProject/factomd/common/primitives"
	"github.com/FactomProject/factomd/database/databaseOverlay"
	"github.com/FactomProject/factomd/database/hybridDB"
	"github.com/FactomProject/factomd/database/mapdb"
	"github.com/FactomProject/factomd/log"
	"github.com/FactomProject/factomd/logger"
	"github.com/FactomProject/factomd/util"
	"github.com/FactomProject/factomd/wsapi"
	"os"
	"strings"
)

var _ = fmt.Print

type State struct {
	filename string

	Cfg interfaces.IFactomConfig

	FactomNodeName          string
	FactomdVersion          int
	ProtocolVersion         int
	LogPath                 string
	LdbPath                 string
	BoltDBPath              string
	LogLevel                string
	ConsoleLogLevel         string
	NodeMode                string
	DBType                  string
	ExportData              bool
	ExportDataSubpath       string
	Network                 string
	LocalServerPrivKey      string
	DirectoryBlockInSeconds int
	PortNumber              int
	Replay                  *Replay

	CoreChainID     interfaces.IHash // The ChainID of the first server when we boot a network.
	IdentityChainID interfaces.IHash // If this node has an identity, this is it

	// Just to print (so debugging doesn't drive functionaility)
	serverPrt string

	timerMsgQueue          chan interfaces.IMsg
	networkOutMsgQueue     chan interfaces.IMsg
	networkInvalidMsgQueue chan interfaces.IMsg
	inMsgQueue             chan interfaces.IMsg
	ShutdownChan           chan int // For gracefully halting Factom

	myServer      interfaces.IServer //the server running on this Federated Server
	serverPrivKey primitives.PrivateKey
	serverPubKey  primitives.PublicKey
	serverState   int
	OutputAllowed bool
	ServerIndex   int // Index of the server, as understood by the leader

	LLeaderHeight uint32

	// Maps
	// ====
	// For Follower
	Holding map[[32]byte]interfaces.IMsg // Hold Messages
	Acks    map[[32]byte]interfaces.IMsg // Hold Acknowledgemets

	AuditHeartBeats []interfaces.IMsg   // The checklist of HeartBeats for this period
	FedServerFaults [][]interfaces.IMsg // Keep a fault list for every server

	//Network MAIN = 0, TEST = 1, LOCAL = 2, CUSTOM = 3
	NetworkNumber int // Encoded into Directory Blocks(s.Cfg.(*util.FactomdConfig)).String()

	// Number of Servers acknowledged by Factom
	Matryoshka []interfaces.IHash // Reverse Hash

	// Database
	DB     *databaseOverlay.Overlay
	Logger *logger.FLogger
	Anchor interfaces.IAnchor

	// Directory Block State
	DBStates *DBStateList // Holds all DBStates not yet processed.

	// Having all the state for a particular directory block stored in one structure
	// makes creating the next state, updating the various states, and setting up the next
	// state much more simple.
	//
	// Functions that provide state information take a dbheight param.  I use the current
	// DBHeight to ensure that I return the proper information for the right directory block
	// height, even if it changed out from under the calling code.
	//
	// Process list previous [0], present(@DBHeight) [1], and future (@DBHeight+1) [2]

	ProcessLists *ProcessLists

	// Factom State
	FactoidState    interfaces.IFactoidState
	NumTransactions int

	// Permanent balances from processing blocks.
	FactoidBalancesP map[[32]byte]int64
	ECBalancesP      map[[32]byte]int64

	// Temporary balances from updating transactions in real time.
	FactoidBalancesT map[[32]byte]int64
	ECBalancesT      map[[32]byte]int64

	FactoshisPerEC uint64
	// Web Services
	Port int
}

var _ interfaces.IState = (*State)(nil)

func (s *State) Clone(number string) interfaces.IState {

	clone := new(State)

	clone.FactomNodeName = "FNode" + number
	clone.FactomdVersion = s.FactomdVersion
	clone.ProtocolVersion = s.ProtocolVersion
	clone.LogPath = s.LogPath + "Sim" + number
	clone.LdbPath = s.LdbPath + "Sim" + number
	clone.BoltDBPath = s.BoltDBPath + "Sim" + number
	clone.LogLevel = s.LogLevel
	clone.ConsoleLogLevel = s.ConsoleLogLevel
	clone.NodeMode = "FULL"
	clone.DBType = s.DBType
	clone.ExportData = s.ExportData
	clone.ExportDataSubpath = s.ExportDataSubpath + "sim-" + number
	clone.Network = s.Network
	clone.DirectoryBlockInSeconds = s.DirectoryBlockInSeconds
	clone.PortNumber = s.PortNumber

	clone.CoreChainID = s.CoreChainID
	clone.IdentityChainID = primitives.Sha([]byte(number))
	// Need to have a Server Priv Key TODO:
	clone.LocalServerPrivKey = s.LocalServerPrivKey

	//serverPrivKey primitives.PrivateKey
	//serverPubKey  primitives.PublicKey

	clone.FactoshisPerEC = s.FactoshisPerEC

	clone.Port = s.Port

	return clone
}

func (s *State) GetFactomNodeName() string {
	return s.FactomNodeName
}

func (s *State) LoadConfig(filename string) {

	s.FactomNodeName = "FNode0" // Default Factom Node Name for Simulation

	if len(filename) > 0 {
		s.filename = filename
		s.ReadCfg(filename)

		// Get our factomd configuration information.
		cfg := s.GetCfg().(*util.FactomdConfig)

		s.LogPath = cfg.Log.LogPath
		s.LdbPath = cfg.App.LdbPath
		s.BoltDBPath = cfg.App.BoltDBPath
		s.LogLevel = cfg.Log.LogLevel
		s.ConsoleLogLevel = cfg.Log.ConsoleLogLevel
		s.NodeMode = cfg.App.NodeMode
		s.DBType = cfg.App.DBType
		s.ExportData = cfg.App.ExportData // bool
		s.ExportDataSubpath = cfg.App.ExportDataSubpath
		s.Network = cfg.App.Network
		s.LocalServerPrivKey = cfg.App.LocalServerPrivKey
		s.FactoshisPerEC = cfg.App.ExchangeRate
		s.DirectoryBlockInSeconds = cfg.App.DirectoryBlockInSeconds
		s.PortNumber = cfg.Wsapi.PortNumber

		// TODO:  Actually load the IdentityChainID from the config file
		s.IdentityChainID = primitives.Sha([]byte("0"))

		// TODO:  CoreChainID is our 'authority chain' that will be used to manage Factom
		// until the network is real, and to serve as the authority to reboot the network
		// should consensus or software or networks fail in some unpredicted way.
		s.CoreChainID = primitives.Sha([]byte("0"))

	} else {
		s.LogPath = "database/"
		s.LdbPath = "database/ldb"
		s.BoltDBPath = "database/bolt"
		s.LogLevel = "none"
		s.ConsoleLogLevel = "standard"
		s.NodeMode = "SERVER"
		s.DBType = "Map"
		s.ExportData = false
		s.ExportDataSubpath = "data/export"
		s.Network = "LOCAL"
		s.LocalServerPrivKey = "4c38c72fc5cdad68f13b74674d3ffb1f3d63a112710868c9b08946553448d26d"
		s.FactoshisPerEC = 00100000
		s.DirectoryBlockInSeconds = 6
		s.PortNumber = 8088

		// TODO:  Actually load the IdentityChainID from the config file
		s.IdentityChainID = primitives.Sha([]byte("0"))

		// TODO:  CoreChainID is our 'authority chain' that will be used to manage Factom
		// until the network is real, and to serve as the authority to reboot the network
		// should consensus or software or networks fail in some unpredicted way.
		s.CoreChainID = primitives.Sha([]byte("0"))
	}
}

func (s *State) Init() {

	wsapi.InitLogs(s.LogPath+s.FactomNodeName+".log", s.LogLevel)

	s.Println("Logger: ", s.LogPath, s.LogLevel)
	s.Logger = logger.NewLogFromConfig(s.LogPath, s.LogLevel, "State")

	log.SetLevel(s.ConsoleLogLevel)

	s.timerMsgQueue = make(chan interfaces.IMsg, 10000)          //incoming eom notifications, used by leaders
	s.networkInvalidMsgQueue = make(chan interfaces.IMsg, 10000) //incoming message queue from the network messages
	s.networkOutMsgQueue = make(chan interfaces.IMsg, 10000)     //Messages to be broadcast to the network
	s.inMsgQueue = make(chan interfaces.IMsg, 10000)             //incoming message queue for factom application messages
	s.ShutdownChan = make(chan int, 1)                           //Channel to gracefully shut down.

	// Set up struct to stop replay attacks
	s.Replay = new(Replay)

	// Set up maps for the followers
	s.Holding = make(map[[32]byte]interfaces.IMsg)
	s.Acks = make(map[[32]byte]interfaces.IMsg)

	// Setup the FactoidState and Validation Service that holds factoid and entry credit balances
	s.FactoidBalancesP = map[[32]byte]int64{}
	s.ECBalancesP = map[[32]byte]int64{}
	s.FactoidBalancesT = map[[32]byte]int64{}
	s.ECBalancesT = map[[32]byte]int64{}

	fs := new(FactoidState)
	fs.State = s
	s.FactoidState = fs

	// Allocate the original set of Process Lists
	s.ProcessLists = NewProcessLists(s)

	s.FactomdVersion = constants.FACTOMD_VERSION
	s.ProtocolVersion = constants.PROTOCOL_VERSION

	s.DBStates = new(DBStateList)
	s.DBStates.State = s
	s.DBStates.DBStates = make([]*DBState, 0)

	switch s.NodeMode {
	case "FULL":
		s.serverState = 0
		s.Println("\n   +---------------------------+")
		s.Println("   +------ Follower Only ------+")
		s.Println("   +---------------------------+\n")
	case "SERVER":
		s.serverState = 1
		s.Println("\n   +-------------------------+")
		s.Println("   |       Leader Node       |")
		s.Println("   +-------------------------+\n")
	default:
		panic("Bad Node Mode (must be FULL or SERVER)")
	}

	//Database
	switch s.DBType {
	case "LDB":
		if err := s.InitLevelDB(); err != nil {
			log.Printfln("Error initializing the database: %v", err)
		}
	case "Bolt":
		if err := s.InitBoltDB(); err != nil {
			log.Printfln("Error initializing the database: %v", err)
		}
	case "Map":
		if err := s.InitMapDB(); err != nil {
			log.Printfln("Error initializing the database: %v", err)
		}
	default:
		panic("No Database type specified")
	}

	if s.ExportData {
		s.DB.SetExportData(s.ExportDataSubpath)
	}

	//Network
	switch s.Network {
	case "MAIN":
		s.NetworkNumber = constants.NETWORK_MAIN
	case "TEST":
		s.NetworkNumber = constants.NETWORK_TEST
	case "LOCAL":
		s.NetworkNumber = constants.NETWORK_LOCAL
	case "CUSTOM":
		s.NetworkNumber = constants.NETWORK_CUSTOM
	default:
		panic("Bad value for Network in factomd.conf")
	}

	s.Println("\nRunning on the ", s.Network, "Network")

	s.AuditHeartBeats = make([]interfaces.IMsg, 0)
	s.FedServerFaults = make([][]interfaces.IMsg, 0)

	a, _ := anchor.InitAnchor(s)
	s.Anchor = a

	s.initServerKeys()
}

func (s *State) LoadDBState(dbheight uint32) (interfaces.IMsg, error) {

	dblk, err := s.DB.FetchDBlockByHeight(dbheight)
	if err != nil {
		return nil, err
	}
	if dblk == nil {
		return nil, nil
	}
	ablk, err := s.DB.FetchABlockByKeyMR(dblk.GetDBEntries()[0].GetKeyMR())
	if err != nil {
		return nil, err
	}
	if ablk == nil {
		return nil, fmt.Errorf("ABlock not found")
	}
	ecblk, err := s.DB.FetchECBlockByHash(dblk.GetDBEntries()[1].GetKeyMR())
	if err != nil {
		return nil, err
	}
	if ecblk == nil {
		return nil, fmt.Errorf("ECBlock not found")
	}
	fblk, err := s.DB.FetchFBlockByKeyMR(dblk.GetDBEntries()[2].GetKeyMR())
	if err != nil {
		return nil, err
	}
	if fblk == nil {
		return nil, fmt.Errorf("FBlock not found")
	}

	// 	fmt.Printf("LoadDBState %s: %d %d dblk: %x ablk: %x/%x ecblk:%x/%x fblk:%x/%x\n",
	// 			   s.FactomNodeName,
	// 			   dblk.GetHeader().GetDBHeight(),
	// 			   dbheight,
	// 			   dblk.GetKeyMR().Bytes()[:5],
	// 			   dblk.GetDBEntries()[0].GetKeyMR().Bytes()[:5],
	// 			   ablk.GetHash().Bytes()[:5],
	// 			   dblk.GetDBEntries()[1].GetKeyMR().Bytes()[:5],
	// 			   ecblk.GetHash().Bytes()[:5],
	// 			   dblk.GetDBEntries()[2].GetKeyMR().Bytes()[:5],
	// 			   fblk.GetHash().Bytes()[:5])

	msg := messages.NewDBStateMsg(s, dblk, ablk, fblk, ecblk)

	return msg, nil

}

func (s *State) GetDBState(height uint32) *DBState {
	return s.DBStates.Get(height)
}

// Return the Directory block if it is in memory, or hit the database if it must
// be loaded.
func (s *State) GetDirectoryBlockByHeight(height uint32) interfaces.IDirectoryBlock {
	dbstate := s.DBStates.Get(height)
	if dbstate != nil {
		return dbstate.DirectoryBlock
	}
	dblk, err := s.DB.FetchDBlockByHeight(height)
	if err != nil {
		return nil
	}
	return dblk
}

func (s *State) UpdateState() {

	s.ProcessLists.UpdateState()
	s.DBStates.UpdateState()

	if s.GetOut() {
		str := fmt.Sprintf("%25s   %10s   %25s", "sssssssssssssssssssssssss", s.GetFactomNodeName(), "sssssssssssssssssssssssss\n")
		str = str + s.ProcessLists.String()
		str = str + s.DBStates.String()
		str = str + fmt.Sprintf("%25s   %10s   %25s", "eeeeeeeeeeeeeeeeeeeeeeeee", s.GetFactomNodeName(), "eeeeeeeeeeeeeeeeeeeeeeeee\n")
		str = str + "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"

		s.Println(str)
	}
}

func (s *State) GetFactoshisPerEC() uint64 {
	return s.FactoshisPerEC
}

func (s *State) SetFactoshisPerEC(factoshisPerEC uint64) {
	s.FactoshisPerEC = factoshisPerEC
}

func (s *State) GetIdentityChainID() interfaces.IHash {
	return s.IdentityChainID
}

func (s *State) GetCoreChainID() interfaces.IHash {
	return s.CoreChainID
}

func (s *State) GetDirectoryBlockInSeconds() int {
	return s.DirectoryBlockInSeconds
}

func (s *State) GetServer() interfaces.IServer {
	return s.myServer
}

func (s *State) SetServer(server interfaces.IServer) {
	s.myServer = server
}

func (s *State) GetServerPrivateKey() primitives.PrivateKey {
	return s.serverPrivKey
}

func (s *State) GetServerPublicKey() primitives.PublicKey {
	return s.serverPubKey
}

func (s *State) GetAnchor() interfaces.IAnchor {
	return s.Anchor
}

func (s *State) GetFactomdVersion() int {
	return s.FactomdVersion
}

func (s *State) GetProtocolVersion() int {
	return s.ProtocolVersion
}

func (s *State) initServerKeys() {
	var err error
	s.serverPrivKey, err = primitives.NewPrivateKeyFromHex(s.LocalServerPrivKey)
	if err != nil {
		//panic("Cannot parse Server Private Key from configuration file: " + err.Error())
	}
	s.serverPubKey = primitives.PubKeyFromString(constants.SERVER_PUB_KEY)
}

func (s *State) LogInfo(args ...interface{}) {
	s.Logger.Info(args...)
}

func (s *State) GetAuditHeartBeats() []interfaces.IMsg {
	return s.AuditHeartBeats
}
func (s *State) GetFedServerFaults() [][]interfaces.IMsg {
	return s.FedServerFaults
}

func (s *State) GetTimestamp() interfaces.Timestamp {
	return *interfaces.NewTimeStampNow()
}

func (s *State) Sign([]byte) interfaces.IFullSignature {
	return new(primitives.Signature)
}

func (s *State) GetFactoidState() interfaces.IFactoidState {
	return s.FactoidState
}

func (s *State) SetFactoidState(dbheight uint32, fs interfaces.IFactoidState) {
	s.FactoidState = fs
}

// Allow us the ability to update the port number at run time....
func (s *State) SetPort(port int) {
	s.PortNumber = port
}

func (s *State) GetPort() int {
	return s.PortNumber
}

func (s *State) TimerMsgQueue() chan interfaces.IMsg {
	return s.timerMsgQueue
}

func (s *State) NetworkInvalidMsgQueue() chan interfaces.IMsg {
	return s.networkInvalidMsgQueue
}

func (s *State) NetworkOutMsgQueue() chan interfaces.IMsg {
	return s.networkOutMsgQueue
}

func (s *State) InMsgQueue() chan interfaces.IMsg {
	return s.inMsgQueue
}

//var _ IState = (*State)(nil)

// Getting the cfg state for Factom doesn't force a read of the config file unless
// it hasn't been read yet.
func (s *State) GetCfg() interfaces.IFactomConfig {
	return s.Cfg
}

// ReadCfg forces a read of the factom config file.  However, it does not change the
// state of any cfg object held by other processes... Only what will be returned by
// future calls to Cfg().(s.Cfg.(*util.FactomdConfig)).String()
func (s *State) ReadCfg(filename string) interfaces.IFactomConfig {
	s.Cfg = util.ReadConfig(filename)
	return s.Cfg
}

func (s *State) GetNetworkNumber() int {
	return s.NetworkNumber
}

func (s *State) GetMatryoshka(dbheight uint32) interfaces.IHash {
	return nil
}

func (s *State) InitLevelDB() error {
	if s.DB != nil {
		return nil
	}

	path := s.LdbPath + "/" + s.Network + "/" + "factoid_level.db"

	s.Println("Database:", path)

	dbase, err := hybridDB.NewLevelMapHybridDB(path, false)

	if err != nil || dbase == nil {
		dbase, err = hybridDB.NewLevelMapHybridDB(path, true)
		if err != nil {
			return err
		}
	}

	s.DB = databaseOverlay.NewOverlay(dbase)
	return nil
}

func (s *State) InitBoltDB() error {
	if s.DB != nil {
		return nil
	}

	path := s.BoltDBPath + "/" + s.Network + "/"

	s.Println("Database Path for", s.FactomNodeName, "is", path)
	os.MkdirAll(path, 0777)
	dbase := hybridDB.NewBoltMapHybridDB(nil, path+"FactomBolt.db")
	s.DB = databaseOverlay.NewOverlay(dbase)
	return nil
}

func (s *State) InitMapDB() error {
	if s.DB != nil {
		return nil
	}

	dbase := new(mapdb.MapDB)
	dbase.Init(nil)
	s.DB = databaseOverlay.NewOverlay(dbase)
	return nil
}

func (s *State) String() string {
	str := "\n===============================================================\n" + s.serverPrt
	str = fmt.Sprintf("\n%s\n  Leader Height: %d\n", str, s.LLeaderHeight)
	str = str + "===============================================================\n"
	return str
}

func (s *State) ShortString() string {
	return s.serverPrt
}

func (s *State) SetString() {
	buildingBlock := s.GetLeaderHeight()
	if buildingBlock == 0 {
		s.serverPrt = fmt.Sprintf("%5s %7s Recorded: %d Building: %d Highest: %d  IDChainID[:5]=%x",
			"",
			s.FactomNodeName,
			s.GetHighestRecordedBlock(),
			0,
			s.GetHighestKnownBlock(),
			s.IdentityChainID.Bytes()[:5])
	} else {
		found, index := s.ProcessLists.Get(buildingBlock).GetFedServerIndex(s.IdentityChainID)
		stype := ""
		if found {
			stype = fmt.Sprintf("L %3d", index)
		}
		keyMR, abHash, fbHash, ecHash := []byte("aaaaa"), []byte("aaaaa"), []byte("aaaaa"), []byte("aaaaa")
		switch {
		case s.DBStates == nil:
			s.serverPrt = ""
			return
		case s.DBStates.Last() == nil:
			s.serverPrt = ""
			return
		case s.DBStates.Last().DirectoryBlock == nil:
			s.serverPrt = ""
			return
		default:
			keyMR = s.DBStates.Last().DirectoryBlock.GetKeyMR().Bytes()
			abHash = s.DBStates.Last().AdminBlock.GetHash().Bytes()
			fbHash = s.DBStates.Last().FactoidBlock.GetHash().Bytes()
			ecHash = s.DBStates.Last().EntryCreditBlock.GetHash().Bytes()
		}
		if s.DBStates.Last().Saved == false {
			return
		}
		ht := uint32(0)
		if s.DBStates.Last() != nil {
			ht = s.DBStates.Last().FactoidBlock.GetDBHeight()
		}
		s.serverPrt = fmt.Sprintf("%5s %7s Recorded: %d Building: %d Highest: %d DirBlk[:5]=%x ABHash[:5]=%x FBHash[:5]=%x %d ECHash[:5]=%x IDChainID[:5]=%x",
			stype,
			s.FactomNodeName,
			s.GetHighestRecordedBlock(),
			buildingBlock,
			s.GetHighestKnownBlock(),
			keyMR[:5],
			abHash[:5],
			fbHash[:5],
			ht,
			ecHash[:5],
			s.IdentityChainID.Bytes()[:5])
	}
}

func (s *State) Print(a ...interface{}) (n int, err error) {
	if s.OutputAllowed {
		str := ""
		for _, v := range a {
			str = str + fmt.Sprintf("%v", v)
		}

		str = strings.Replace(str, "\n", "\r\n", -1)
		return fmt.Print(str)
	}

	return 0, nil
}

func (s *State) Println(a ...interface{}) (n int, err error) {
	if s.OutputAllowed {
		str := ""
		for _, v := range a {
			str = str + fmt.Sprintf("%v", v)
		}
		str = str + "\n"

		str = strings.Replace(str, "\n", "\r\n", -1)

		return fmt.Print(str)
	}

	return 0, nil
}

func (s *State) GetOut() bool {
	return s.OutputAllowed
}

func (s *State) SetOut(o bool) {
	s.OutputAllowed = o
}
