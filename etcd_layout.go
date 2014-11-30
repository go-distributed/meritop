package meritop

import (
	"path"
	"strconv"
)

// The directory layout we going to define in etcd:
//   /{app}/config -> application configuration
//   /{app}/epoch -> global value for epoch
//   /{app}/tasks/: register tasks under this directory
//   /{app}/tasks/{taskID}/{replicaID} -> pointer to nodes, 0 replicaID means master
//   /{app}/tasks/{taskID}/parentMeta
//   /{app}/tasks/{taskID}/childMeta
//   /{app}/nodes/: register nodes under this directory
//   /{app}/nodes/{nodeID}/address -> scheme://host:port/{path(if http)}
//   /{app}/nodes/{nodeID}/ttl -> keep alive timeout

const (
	TasksDir       = "tasks"
	NodesDir       = "nodes"
	ConfigDir      = "config"
	Epoch          = "epoch"
	TaskMaster     = "0"
	TaskParentMeta = "ParentMeta"
	TaskChildMeta  = "ChildMeta"
	NodeAddr       = "address"
	NodeTTL        = "ttl"
)

func JobEpochPath(appName string) string {
	return path.Join("/",
		appName,
		Epoch)
}

func TaskDirPath(appName string) string {
	return path.Join("/", appName, TasksDir)
}

func TaskMasterPath(appName string, taskID uint64) string {
	return path.Join("/", appName, TasksDir, strconv.FormatUint(taskID, 10), TaskMaster)
}

func ParentMetaPath(appName string, taskID uint64) string {
	return path.Join("/",
		appName,
		TasksDir,
		strconv.FormatUint(taskID, 10),
		TaskParentMeta)
}

func ChildMetaPath(appName string, taskID uint64) string {
	return path.Join("/",
		appName,
		TasksDir,
		strconv.FormatUint(taskID, 10),
		TaskChildMeta)
}
