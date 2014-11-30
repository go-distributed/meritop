package framework

import (
	"log"
	"net"
	"net/http"
	"os"
	"strconv"

	"github.com/coreos/go-etcd/etcd"
	"github.com/go-distributed/meritop"
	"github.com/go-distributed/meritop/pkg/etcdutil"
)

// One need to pass in at least these two for framework to start. The config
// is used to pass on to task implementation for its configuration.
func NewBootStrap(jobName string, etcdURLs []string, config meritop.Config, ln net.Listener, logger *log.Logger) meritop.Bootstrap {
	return &framework{
		name:     jobName,
		etcdURLs: etcdURLs,
		config:   config,
		ln:       ln,
		log:      logger,
	}
}

func (f *framework) SetTaskBuilder(taskBuilder meritop.TaskBuilder) { f.taskBuilder = taskBuilder }

func (f *framework) SetTopology(topology meritop.Topology) { f.topology = topology }

func (f *framework) Start() {
	var err error

	if f.log == nil {
		f.log = log.New(os.Stdout, "", log.Lshortfile|log.Ltime|log.Ldate)
	}

	// First, we fetch the current global epoch from etcd.
	f.epoch, err = f.fetchEpoch()
	if err != nil {
		f.log.Fatal("Can not parse epoch from etcd")
	}

	if f.taskID, err = f.occupyTask(); err != nil {
		f.log.Fatalf("occupyTask failed: %v", err)
	}

	// task builder and topology are defined by applications.
	// Both should be initialized at this point.
	// Get the task implementation and topology for this node (indentified by taskID)
	f.task = f.taskBuilder.GetTask(f.taskID)
	f.topology.SetTaskID(f.taskID)

	// setup etcd watches
	// - create self's parent and child meta flag
	// - watch parents' child meta flag
	// - watch children's parent meta flag
	f.etcdClient.Create(etcdutil.MakeParentMetaPath(f.name, f.GetTaskID()), "", 0)
	f.etcdClient.Create(etcdutil.MakeChildMetaPath(f.name, f.GetTaskID()), "", 0)
	f.watchAll(roleParent, f.topology.GetParents(f.epoch))
	f.watchAll(roleChild, f.topology.GetChildren(f.epoch))

	// We need to first watch epoch.
	f.watchEpoch()

	go f.startHTTP()
	f.dataRespChan = make(chan *dataResponse, 100)
	go f.dataResponseReceiver()

	// After framework init finished, it should init task.
	f.task.Init(f.taskID, f, f.config)

	for f.epoch != maxUint64 {
		f.task.SetEpoch(f.epoch)
		select {
		case f.epoch = <-f.epochChan:
			// TODO: cleanup resources.
		case <-f.epochStop:
			return
		}
	}

	// clean up resources
	f.stop()
}

// Framework http server for data request.
// Each request will be in the format: "/datareq?taskID=XXX&req=XXX".
// "taskID" indicates the requesting task. "req" is the meta data for this request.
// On success, it should respond with requested data in http body.
func (f *framework) startHTTP() {
	f.log.Printf("serving http on %s", f.ln.Addr())
	// TODO: http server graceful shutdown
	if err := http.Serve(f.ln, &dataReqHandler{f}); err != nil {
		f.log.Fatalf("http.Serve() returns error: %v\n", err)
	}
}

func (f *framework) watchAll(who taskRole, taskIDs []uint64) {
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
			watchPath = etcdutil.MakeChildMetaPath(f.name, taskID)
			taskCallback = f.task.ParentMetaReady
		case roleChild:
			// Watch child's parent.
			watchPath = etcdutil.MakeParentMetaPath(f.name, taskID)
			taskCallback = f.task.ChildMetaReady
		default:
			panic("unimplemented")
		}

		go f.etcdClient.Watch(watchPath, 1, false, receiver, stop)
		go func(receiver <-chan *etcd.Response, taskID uint64) {
			for resp := range receiver {
				if resp.Action != "set" {
					continue
				}
				taskCallback(taskID, resp.Node.Value)
			}
		}(receiver, taskID)
	}
	f.stops = append(f.stops, stops...)
}

func (f *framework) fetchEpoch() (uint64, error) {
	f.etcdClient = etcd.NewClient(f.etcdURLs)

	epochPath := etcdutil.MakeJobEpochPath(f.name)
	resp, err := f.etcdClient.Get(epochPath, false, false)
	if err != nil {
		f.log.Fatal("Can not get epoch from etcd")
	}
	return strconv.ParseUint(resp.Node.Value, 10, 64)
}

func (f *framework) watchEpoch() {
	receiver := make(chan *etcd.Response, 1)
	f.epochChan = make(chan uint64, 1)
	f.epochStop = make(chan bool, 1)

	watchPath := etcdutil.MakeJobEpochPath(f.name)
	go f.etcdClient.Watch(watchPath, 1, false, receiver, f.epochStop)
	go func(receiver <-chan *etcd.Response) {
		for resp := range receiver {
			if resp.Action != "compareAndSwap" && resp.Action != "set" {
				continue
			}
			epoch, err := strconv.ParseUint(resp.Node.Value, 10, 64)
			if err != nil {
				f.log.Fatal("Can't parse epoch from etcd")
			}
			f.epochChan <- epoch
		}
	}(receiver)
}