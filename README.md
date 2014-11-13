TaskGraph
=========

[![Build Status](https://travis-ci.org/go-distributed/meritop.svg)](https://travis-ci.org/go-distributed/meritop)


TaskGraph is a framework for writing fault-tolerent distributed applications. It assumes that an application is a network of tasks, where the topology of the network can change during the execution of the application. 

The TaskGraph framework monitors tasks's health status, and takes care of restarting failed tasks. It also notify tasks about events, including parent failureand restart, children failure and restart, so to make a chance for application-specific recovery.


A TaskGraph application consists of three parts:

1. The *driver*, usually the `main` function, which

   1. sets up *TaskBuilder*, which maps tasks to nodes,
   1. specifies network topology at each iteration, and
   1. calls `FrameWork.Start` to start event loop of every node. 

2. The *TaskGraph framework*, which handles fault recovery using `etcd` and `Kubernetes`.  It also provides API to ease inter-task communication.

3. An implementation of the `Task` interface, which programs reactions to events sent by the framework, including failures and restart of parent node and children nodes, and dia and restart of the current task.  Note that application developer also need to implement the `TaskBuilder.Topology` interface as required by the driver.

For an example of driver and task implementation, please refer to `dummy_task.go`.

Please note that we might redefine how applications should terminate themselves in the near future.  