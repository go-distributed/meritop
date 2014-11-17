package meritop

type UserData interface{}

// Task is a logic repersentation of a computing unit.
// Each task contain at least one Node.
// Each task has exact one master Node and might have multiple salve Nodes.
type Task interface {
	// This is useful to bring the task up to speed from scratch or if it recovers.
	Init(taskID uint64, framework Framework, config Config)

	// Task need to finish up for exit, last chance to save work?
	Exit()

	// Ideally, we should also have the following:
	ParentMetaReady(parentID uint64, meta string)
	ChildMetaReady(childID uint64, meta string)

	// This give the task an opportunity to cleanup and regroup.
	SetEpoch(epoch uint64)

	// These are payload for application purpose.
	ServeAsParent(req string) UserData
	ServeAsChild(reg string) UserData

	ParentDataReady(fromID uint64, req string, response UserData)
	ChildDataReady(fromID uint64, req string, response UserData)
}

// We should not try to stay away from the stateful task as much as possible.
// The state of the job should be fully encoded in epoch/topo and meta from neighbors.
type StatefulTask interface {
	// These are called by framework implementation so that task implementation can
	// reacts to parent or children restart.
	ParentRestart(parentID uint64)
	ChildRestart(childID uint64)

	ParentDie(parentID uint64)
	ChildDie(childID uint64)
}

type UpdateLog interface {
	UpdateID()
}

// Backupable is an interface that task need to implement if they want to have
// hot standby copy. This is another can of beans.
type Backupable interface {
	// Some hooks that need for master slave etc.
	BecamePrimary()
	BecameBackup()

	// Framework notify this copy to update. This should be the only way that
	// one update the state of copy.
	Update(log UpdateLog)
}
