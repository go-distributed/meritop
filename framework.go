package meritop

import (
	"log"

	"github.com/coreos/go-etcd/etcd"
)

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

	// Some task can inform all participating tasks to new epoch
	SetEpoch(epoch uint64)

	GetLogger() log.Logger

	// Request data from parent or children.
	DataRequest(toID uint64, meta string)

	// This is used to figure out taskid for current node
	GetTaskID() uint64
}

// TODO: separate framework and user meta-data
type Context struct {
	epoch, fromID, toID, uuID uint64
	meta                      string
}

type framework struct {
	// These should be passed by outside world
	name     string
	etcdURLs []string

	// user defined interfaces
	task     Task
	topology Topology

	taskID     uint64
	epoch      uint64
	etcdClient *etcd.Client
	stops      []chan bool
}

func (f *framework) start() {
	f.etcdClient = etcd.NewClient(f.etcdURLs)
	f.topology.SetTaskID(f.taskID)
	f.epoch = 0
	f.stops = make([]chan bool, 0)

	// setup etcd watches
	// - create self's parent and child meta flag
	// - watch parents' child meta flag
	// - watch children's parent meta flag
	f.etcdClient.Create(MakeTaskParentMetaPath(f.name, f.GetTaskID()), "", 0)
	f.etcdClient.Create(MakeTaskChildMetaPath(f.name, f.GetTaskID()), "", 0)
	parentStops := f.watchAll("parent", f.topology.GetParents(f.epoch))
	childStops := f.watchAll("child", f.topology.GetChildren(f.epoch))

	f.stops = append(f.stops, parentStops...)
	f.stops = append(f.stops, childStops...)

	// After framework init finished, it should init task.
	f.task.SetEpoch(f.epoch)
	f.task.Init(f.taskID, f, nil)
}

func (f *framework) stop() {
	for _, c := range f.stops {
		close(c)
	}
}

func (f *framework) FlagParentMetaReady(meta string) {
	f.etcdClient.Set(
		MakeTaskParentMetaPath(f.name, f.GetTaskID()),
		"",
		0)
}

func (f *framework) FlagChildMetaReady(meta string) {
	f.etcdClient.Set(
		MakeTaskChildMetaPath(f.name, f.GetTaskID()),
		"",
		0)
}

func (f *framework) SetEpoch(epoch uint64) {
	f.epoch = epoch
}

func (f *framework) watchAll(who string, taskIDs []uint64) []chan bool {
	stops := make([]chan bool, len(taskIDs))

	for i, taskID := range taskIDs {
		receiver := make(chan *etcd.Response, 10)
		stop := make(chan bool, 1)
		stops[i] = stop

		var watchPath string
		var taskCallback func(uint64, string)
		switch who {
		case "parent":
			// Watch parent's child.
			watchPath = MakeTaskChildMetaPath(f.name, taskID)
			taskCallback = f.task.ParentMetaReady
		case "child":
			// Watch child's parent.
			watchPath = MakeTaskParentMetaPath(f.name, taskID)
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
				taskCallback(taskID, "")
			}
		}(receiver, taskID)
	}
	return stops
}

func (f *framework) DataRequest(toID uint64, meta string) {
}

func (f *framework) GetTopology() Topology {
	panic("unimplemented")
}

func (f *framework) Exit() {
}

func (f *framework) GetLogger() log.Logger {
	panic("unimplemented")
}

func (f *framework) GetTaskID() uint64 {
	return f.taskID
}
