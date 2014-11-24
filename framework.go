package meritop

import (
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"

	"github.com/coreos/go-etcd/etcd"
)

type taskRole int

const (
	roleNone taskRole = iota
	roleParent
	roleChild
)

const (
	dataRequestPrefix string = "/datareq"
	dataRequestTaskID string = "taskID"
	dataRequestReq    string = "req"
)

// This is used as special value to indicate that it is the last epoch, time
// to exit.
const maxUint64 uint64 = ^uint64(0)

// This interface is used by application during taskgraph configuration phase.
type Bootstrap interface {
	// These allow application developer to set the task configuration so framework
	// implementation knows which task to invoke at each node.
	SetTaskBuilder(taskBuilder TaskBuilder)

	// This allow the application to specify how tasks are connection at each epoch
	SetTopology(topology Topology)

	// After all the configure is done, driver need to call start so that all
	// nodes will get into the event loop to run the application.
	Start()
}

// Note that framework can decide how update can be done, and how to serve the updatelog.
type BackedUpFramework interface {
	// Ask framework to do update on this update on this task, which consists
	// of one primary and some backup copies.
	Update(taskID uint64, log UpdateLog)
}

type Framework interface {
	// These two are useful for task to inform the framework their status change.
	// metaData has to be really small, since it might be stored in etcd.
	// Flags that parent/child's metadata of the current task is ready.
	FlagParentMetaReady(meta string)
	FlagChildMetaReady(meta string)

	// This allow the task implementation query its neighbors.
	GetTopology() Topology

	// Some task can inform all participating tasks to exit.
	Exit()

	// This method will result in local node abort, the same task can be
	// retried by some other node. Only useful for panic inside user code.
	AbortTask()

	// Some task can inform all participating tasks to new epoch
	IncEpoch()

	GetLogger() log.Logger

	// Request data from parent or children.
	DataRequest(toID uint64, meta string)

	// This is used to figure out taskid for current node
	GetTaskID() uint64
}

// One need to pass in at least these two for framework to start. The config
// is used to pass on to task implementation for its configuration.
func NewBootStrap(jobName string, etcdURLs []string, config Config) Bootstrap {
	return &framework{
		name:     jobName,
		etcdURLs: etcdURLs,
		config:   config,
	}
}

type framework struct {
	// These should be passed by outside world
	name     string
	etcdURLs []string
	config   Config

	// user defined interfaces
	taskBuilder TaskBuilder
	topology    Topology

	task          Task
	taskID        uint64
	epoch         uint64
	etcdClient    *etcd.Client
	stops         []chan bool
	ln            net.Listener
	addressMap    map[uint64]string // taskId -> node address. Maybe in etcd later.
	dataRespChan  chan *dataResponse
	dataCloseChan chan struct{}
}

type dataResponse struct {
	taskID uint64
	req    string
	data   []byte
}

func (f *framework) SetTaskBuilder(taskBuilder TaskBuilder) {
	f.taskBuilder = taskBuilder
}

func (f *framework) SetTopology(topology Topology) {
	f.topology = topology
}

func (f *framework) parentOrChild(taskID uint64) taskRole {
	for _, id := range f.topology.GetParents(f.epoch) {
		if taskID == id {
			return roleParent
		}
	}

	for _, id := range f.topology.GetChildren(f.epoch) {
		if taskID == id {
			return roleChild
		}
	}
	return roleNone
}

func (f *framework) Start() {
	f.etcdClient = etcd.NewClient(f.etcdURLs)

	// TODO:
	// a. We need to get epoch from etcd.
	// b. We need to get taskID from etcd.
	f.epoch = 0
	f.stops = make([]chan bool, 0)
	f.dataRespChan = make(chan *dataResponse, 100)
	f.dataCloseChan = make(chan struct{})

	// task builder and topology are defined by applications.
	// Both should be initialized at this point.
	// Get the task implementation and topology for this node (indentified by taskID)
	f.task = f.taskBuilder.GetTask(f.taskID)
	f.topology.SetTaskID(f.taskID)

	// setup etcd watches
	// - create self's parent and child meta flag
	// - watch parents' child meta flag
	// - watch children's parent meta flag
	f.etcdClient.Create(MakeParentMetaPath(f.name, f.GetTaskID()), "", 0)
	f.etcdClient.Create(MakeChildMetaPath(f.name, f.GetTaskID()), "", 0)
	parentStops := f.watchAll(roleParent, f.topology.GetParents(f.epoch))
	childStops := f.watchAll(roleChild, f.topology.GetChildren(f.epoch))
	f.stops = append(f.stops, parentStops...)
	f.stops = append(f.stops, childStops...)

	go f.startHttp()

	// After framework init finished, it should init task.
	f.task.Init(f.taskID, f, f.config)

	// Get into endless loop.
	for f.epoch != maxUint64 {
		f.task.SetEpoch(f.epoch)
		select {
		case dataResp := <-f.dataRespChan:
			switch f.parentOrChild(dataResp.taskID) {
			case roleParent:
				go f.task.ParentDataReady(dataResp.taskID, dataResp.req, dataResp.data)
			case roleChild:
				go f.task.ChildDataReady(dataResp.taskID, dataResp.req, dataResp.data)
			default:
				panic("unimplemented")
			}
		case <-f.dataCloseChan:
			return
		}
	}
}

func (f *framework) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != dataRequestPrefix {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	// parse url query
	q := r.URL.Query()
	fromIDStr := q.Get(dataRequestTaskID)
	fromID, err := strconv.ParseUint(fromIDStr, 0, 64)
	if err != nil {
		http.Error(w, "taskID couldn't be parsed", http.StatusBadRequest)
		return
	}
	req := q.Get(dataRequestReq)
	// ask task to serve data
	var b []byte
	switch f.parentOrChild(fromID) {
	case roleParent:
		b = f.task.ServeAsChild(fromID, req)
	case roleChild:
		b = f.task.ServeAsParent(fromID, req)
	default:
		http.Error(w, "taskID isn't a parent or child of this task", http.StatusBadRequest)
		return
	}

	if _, err := w.Write(b); err != nil {
		log.Printf("response write errored: %v", err)
	}
}

// Framework http server for data request.
// Each request will be in the format: "/datareq?taskID=XXX&req=XXX".
// "taskID" indicates the requesting task. "req" is the meta data for this request.
// On success, it should respond with requested data in http body.
func (f *framework) startHttp() {
	log.Printf("framework: serving http on %s", f.ln.Addr())
	if err := http.Serve(f.ln, f); err != nil {
		log.Fatalf("http.Serve() returns error: %v\n", err)
	}
}

func (f *framework) stop() {
	close(f.dataCloseChan)
	for _, c := range f.stops {
		close(c)
	}
}

func (f *framework) FlagParentMetaReady(meta string) {
	f.etcdClient.Set(
		MakeParentMetaPath(f.name, f.GetTaskID()),
		meta,
		0)
}

func (f *framework) FlagChildMetaReady(meta string) {
	f.etcdClient.Set(
		MakeChildMetaPath(f.name, f.GetTaskID()),
		meta,
		0)
}

func (f *framework) IncEpoch() {
	f.epoch += 1
}

func (f *framework) watchAll(who taskRole, taskIDs []uint64) []chan bool {
	stops := make([]chan bool, len(taskIDs))

	for i, taskID := range taskIDs {
		receiver := make(chan *etcd.Response, 10)
		stop := make(chan bool, 1)
		stops[i] = stop

		var watchPath string
		var taskCallback func(uint64, string)
		switch who {
		case roleParent:
			// Watch parent's child.
			watchPath = MakeChildMetaPath(f.name, taskID)
			taskCallback = f.task.ParentMetaReady
		case roleChild:
			// Watch child's parent.
			watchPath = MakeParentMetaPath(f.name, taskID)
			taskCallback = f.task.ChildMetaReady
		default:
			panic("unimplemented")
		}

		go f.etcdClient.Watch(watchPath, 0, false, receiver, stop)
		go func(receiver <-chan *etcd.Response, taskID uint64) {
			for {
				resp, ok := <-receiver
				if !ok {
					return
				}
				if resp.Action != "set" {
					continue
				}
				taskCallback(taskID, resp.Node.Value)
			}
		}(receiver, taskID)
	}
	return stops
}

func (f *framework) DataRequest(toID uint64, req string) {
	// getAddressFromTaskID
	addr, ok := f.addressMap[toID]
	if !ok {
		log.Fatalf("ID = %d not found", toID)
		return
	}
	u := url.URL{
		Scheme: "http",
		Host:   addr,
		Path:   dataRequestPrefix,
	}
	q := u.Query()
	q.Add(dataRequestTaskID, strconv.FormatUint(f.taskID, 10))
	q.Add(dataRequestReq, req)
	u.RawQuery = q.Encode()
	urlStr := u.String()
	// send request
	// pass the response to the awaiting event loop for data response
	go func(urlStr string) {
		resp, err := http.Get(urlStr)
		if err != nil {
			log.Fatalf("http.Get(%s) returns error: %v", urlStr, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			log.Fatalf("response code = %d, assume = %d", resp.StatusCode, 200)
		}
		data, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Fatalf("ioutil.ReadAll(%v) returns error: %v", resp.Body, err)
		}
		dataResp := &dataResponse{
			taskID: toID,
			req:    req,
			data:   data,
		}
		f.dataRespChan <- dataResp
	}(urlStr)
}

func (f *framework) GetTopology() Topology {
	panic("unimplemented")
}

func (f *framework) Exit() {
}

func (f *framework) AbortTask() {
	panic("unimplemented")
}

func (f *framework) GetLogger() log.Logger {
	panic("unimplemented")
}

func (f *framework) GetTaskID() uint64 {
	return f.taskID
}
