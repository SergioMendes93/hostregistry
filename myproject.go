package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"sync"
	"time"
	"math"
	"strconv"	

	"github.com/docker/swarm/cluster"
	"github.com/docker/swarm/scheduler/node"
	"github.com/gorilla/mux"
)

// Node is an abstract type used by the scheduler.
type Node struct {
	ID         string
	IP         string
	Addr       string
	Name       string
	Labels     map[string]string
	Containers cluster.Containers
	Images     []*cluster.Image

	UsedMemory  int64
	UsedCpus    int64
	TotalMemory int64
	TotalCpus   int64

	HealthIndicator int64
}

type Host struct {
	HostIP                    string       `json:"hostip, omitempty"`
	WorkerNodes               []*node.Node `json:"workernode,omitempty"`
	HostClass                 string       `json:"hostclass,omitempty"`
	Region                    string       `json:"region,omitempty"`
	TotalResourcesUtilization string       `json:"totalresouces,omitempty"`
	CPU_Utilization           string       `json:"cpu,omitempty"`
	MemoryUtilization         string       `json:"memory,omitempty"`
	AllocatedMemory           float64      `json:"allocatedmemory,omitempty"`
	AllocatedCPUs             float64      `json:"allocatedcpus,omitempty"`
	OverbookingFactor         float64      `json:"overbookingfactor,omitempty"`
	TotalMemory				  float64	   `json:"totalmemory,omitempty"`
	TotalCPUs				  float64	   `json:"totalcpus, omitempty"`
}

type TaskResources struct {
	CPU		float64		`json:"cpu, omitempty"`
	Memory 	float64		`json:"memory,omitempty"`
}

//Each region will have 4 lists, one for each overbooking class
//LEE=Lowest Energy Efficiency, DEE =Desired Energy Efficiency EED=Energy Efficiency Degradation
type Region struct {
	classHosts map[string][]*Host
}

type Lock struct {
	classHosts map[string]*sync.Mutex //to lock at class level
	lock	*sync.Mutex //to lock at region level
}

var regions map[string]Region

var hosts map[string]*Host

var locks map[string]Lock //for locking access to regions/class

//adapted binary search algorithm for inserting orderly based on total resources of a host
//this is ascending order (for EED region)
func Sort(classList []*Host, searchValue string) int {
	listLength := len(classList)
	lowerBound := 0
	upperBound := listLength - 1

	if listLength == 0 { //if the list is empty there is no need for sorting
		return 0
	}

	for {
		midPoint := (upperBound + lowerBound) / 2

		if lowerBound > upperBound && classList[midPoint].TotalResourcesUtilization > searchValue {
			return midPoint
		} else if lowerBound > upperBound {
			return midPoint + 1
		}

		if classList[midPoint].TotalResourcesUtilization < searchValue {
			lowerBound = midPoint + 1
		} else if classList[midPoint].TotalResourcesUtilization > searchValue {
			upperBound = midPoint - 1
		} else if classList[midPoint].TotalResourcesUtilization == searchValue {
			return midPoint
		}
	}
}

//for LEE and DEE regions, since they are ordered by descending order the sort above must be reversed
func ReverseSort(classList []*Host, searchValue string) int {
	listLength := len(classList)
	lowerBound := 0
	upperBound := listLength - 1
		
	if listLength == 0 { //if the list is empty there is no need for sorting
		return 0
	}

	for {
		midPoint := (upperBound + lowerBound) / 2
		
		if lowerBound > upperBound && classList[midPoint].TotalResourcesUtilization < searchValue {
			return midPoint
		} else if lowerBound > upperBound {
			return midPoint + 1
		}

		if classList[midPoint].TotalResourcesUtilization > searchValue {
			lowerBound = midPoint + 1
		} else if classList[midPoint].TotalResourcesUtilization < searchValue {
			upperBound = midPoint - 1
		} else if classList[midPoint].TotalResourcesUtilization == searchValue {
			return midPoint
		}
	}
}

func RescheduleTask(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	cpu := params["cpu"]
	memory := params["memory"]
	requestClass := params["requestclass"]
	image := params["image"]
	
	cmd := "docker"
	args := []string{"-H", "tcp://0.0.0.0:2376","run", "-itd", "-c", cpu, "-m", memory, "-e", "affinity:requestclass==" + requestClass, image}

	if err := exec.Command(cmd, args...).Run(); err != nil {
		fmt.Println("Error using docker run")
		fmt.Println(err)
	}
}

func KillTasks(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	taskID := params["taskid"]
	taskCPU := params["taskcpu"]
	taskMemory := params["taskmemory"]
	hostIP := params["hostip"] //ip of the host that contained this task

	cmd := "docker"
	args := []string{"-H", "tcp://0.0.0.0:2376","kill", taskID}

	if err := exec.Command(cmd, args...).Run(); err != nil {
		fmt.Println("Error using docker update")
		fmt.Println(err)
	}

	cpu,_ := strconv.ParseFloat(taskCPU,64)
	memory,_ := strconv.ParseFloat(taskMemory,64)	

	go UpdateResources(cpu, memory, hostIP)
}

//function responsible to update task resources when there's a cut. It will also update the allocated cpu/memory of the host
func UpdateTaskResources(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	taskID := params["taskid"]
	newCPU := params["newcpu"]
	newMemory := params["newmemory"]
	hostIP := params["hostip"]
	cpuCut := params["cpucut"]
	memoryCut := params["memorycut"]

	//TODO ver se este sleep é realmente preciso
	time.Sleep(time.Second * 2)

	//update the task with cut resources
	cmd := "docker"
	args := []string{"-H", "tcp://0.0.0.0:2376","update", "-m", newMemory, "-c", newCPU, taskID}

	if err := exec.Command(cmd, args...).Run(); err != nil {
		fmt.Println("Error using docker update")
		fmt.Println(err)
	}

	//now to update the resources of the host. Because of the cut, less resources will be occupied on the host
		
	memoryReduction, _ := strconv.ParseFloat(memoryCut,64)
	cpuReduction, _ := strconv.ParseFloat(cpuCut,64)
	auxHost := hosts[hostIP]

    locks[auxHost.Region].classHosts[auxHost.HostClass].Lock()
    
    hosts[hostIP].AllocatedMemory -= memoryReduction
    hosts[hostIP].AllocatedCPUs -= cpuReduction

    locks[auxHost.Region].classHosts[auxHost.HostClass].Unlock()
}

func CreateHost(w http.ResponseWriter, req *http.Request) {

/*	var host Host
	_ = json.NewDecoder(req.Body).Decode(&host)

	//since a host is created it will not have tasks assigned to it so it goes to the LEE region to the less restrictive class
*/
	params := mux.Vars(req)
	hostIP := params["hostip"]
	totalMemory,_ := strconv.ParseFloat(params["totalmemory"],64)
	totalCPUs,_ := strconv.ParseFloat(params["totalcpu"],64) 
	totalCPUs *= 1024 // *1024 because 1024 shares equals using 1 cpu by 100%	
	
	locks["LEE"].classHosts["4"].Lock()
	hosts[hostIP] = &Host{HostIP: hostIP, HostClass: "4", Region: "LEE", TotalMemory: totalMemory, TotalCPUs: totalCPUs, AllocatedMemory: 0.0, AllocatedCPUs: 0.0,
	TotalResourcesUtilization: "0.0", CPU_Utilization: "0.0", MemoryUtilization: "0.0", OverbookingFactor:0.0}
	
	newHost := make([]*Host, 0)
	newHost = append(newHost, hosts[hostIP])
	
	regions["LEE"].classHosts["4"] = append(regions["LEE"].classHosts["4"], newHost...)
	locks["LEE"].classHosts["4"].Unlock()

}

//function used to associate a worker to a host when the worker is created
func AddWorker(w http.ResponseWriter, req *http.Request) {
	/*	params := mux.Vars(req)
		workerID := params["workerid"]
	/*
				//TODO: por return aqui
			}
		}*/
	//PARA UMA FASE DE TESTES

	var newWorker *node.Node
	_ = json.NewDecoder(req.Body).Decode(&newWorker)
	addWorker := make([]*node.Node, 0)
	addWorker = append(addWorker, newWorker)

	locks[hosts[newWorker.IP].Region].classHosts[hosts[newWorker.IP].HostClass].Lock()	
	hosts[newWorker.IP].WorkerNodes = append([]*node.Node{newWorker},hosts[newWorker.IP].WorkerNodes...)
	locks[hosts[newWorker.IP].Region].classHosts[hosts[newWorker.IP].HostClass].Unlock()	

}

//function used to update host class when a new task arrives
//implies list change
func UpdateHostClass(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	newHostClass := params["requestclass"]
	hostIP := params["hostip"]


	locks[hosts[hostIP].Region].classHosts[hosts[hostIP].HostClass].Lock()
	currentClass := hosts[hostIP].HostClass

	host := hosts[hostIP]
	if host.HostClass > newHostClass { //we only update the host class if the current class is higher
		hosts[hostIP].HostClass = newHostClass
		locks[hosts[hostIP].Region].classHosts[currentClass].Unlock()
		//we need to update the list where this host is at
		UpdateHostList(host.HostClass, newHostClass, hosts[host.HostIP])
		return
	}
	locks[hosts[hostIP].Region].classHosts[hosts[hostIP].HostClass].Unlock()

}

func InsertHost(classHosts []*Host, index int, host *Host) []*Host {
	tmp := make([]*Host, 0)
	if index >= len(classHosts) { //if this is true then we put at end
		tmp = append(tmp, classHosts...)
		tmp = append(tmp, host)
	} else { //the code below is to insert into the index positin
		tmp = append(tmp, classHosts[:index]...)
		tmp = append(tmp, host)
		tmp = append(tmp, classHosts[index:]...)
	}
	return tmp
}

//this function needs to remove the host from its previous class and update it to the new
func UpdateHostList(hostPreviousClass string, hostNewClass string, host *Host) {
	//this deletes
	locks[host.Region].classHosts[hostPreviousClass].Lock()
	for i := 0; i < len(regions[host.Region].classHosts[hostPreviousClass]); i++ {
		if regions[host.Region].classHosts[hostPreviousClass][i].HostIP == host.HostIP {
			regions[host.Region].classHosts[hostPreviousClass] = append(regions[host.Region].classHosts[hostPreviousClass][:i], regions[host.Region].classHosts[hostPreviousClass][i+1:]...)
			break
		}
	}
	locks[host.Region].classHosts[hostPreviousClass].Unlock()

	//this inserts in new list
	if host.Region == "LEE" || host.Region == "DEE" {
		locks[host.Region].classHosts[hostNewClass].Lock()
		index := ReverseSort(regions[host.Region].classHosts[hostNewClass], host.TotalResourcesUtilization)
		regions[host.Region].classHosts[hostNewClass] = InsertHost(regions[host.Region].classHosts[hostNewClass], index, host)
		locks[host.Region].classHosts[hostNewClass].Unlock()
	} else {
		locks[host.Region].classHosts[hostNewClass].Lock()
		index := Sort(regions[host.Region].classHosts[hostNewClass], host.TotalResourcesUtilization)
		regions[host.Region].classHosts[hostNewClass] = InsertHost(regions[host.Region].classHosts[hostNewClass], index, host)
		locks[host.Region].classHosts[hostNewClass].Unlock()
	}
}

//implies list change
func UpdateHostRegion(hostIP string, newRegion string) {
	fmt.Println("Updating region")

	locks[hosts[hostIP].Region].classHosts[hosts[hostIP].HostClass].Lock()
	oldRegion := hosts[hostIP].Region
	
	fmt.Println(oldRegion)
	fmt.Println(newRegion)

	hosts[hostIP].Region = newRegion
	locks[oldRegion].classHosts[hosts[hostIP].HostClass].Unlock()

	UpdateHostRegionList(hosts[hostIP].Region, newRegion, hosts[hostIP])
	return
}

//first we must remove the host from the previous region then insert it in the new onw
func UpdateHostRegionList(oldRegion string, newRegion string, host *Host) {
	//this deletes
	locks[oldRegion].classHosts[host.HostClass].Lock()
	for i := 0; i < len(regions[oldRegion].classHosts[host.HostClass]); i++ {
		if regions[oldRegion].classHosts[host.HostClass][i].HostIP == host.HostIP {
			regions[oldRegion].classHosts[host.HostClass] = append(regions[oldRegion].classHosts[host.HostClass][:i], regions[oldRegion].classHosts[host.HostClass][i+1:]...)
			break
		}
	}
	locks[oldRegion].classHosts[host.HostClass].Unlock()

	//this inserts in new list
	if newRegion == "LEE" || newRegion == "DEE" {
		locks[newRegion].classHosts[host.HostClass].Lock()
		index := ReverseSort(regions[newRegion].classHosts[host.HostClass], host.TotalResourcesUtilization)		
		regions[newRegion].classHosts[host.HostClass] = InsertHost(regions[newRegion].classHosts[host.HostClass], index, host)
		locks[newRegion].classHosts[host.HostClass].Unlock()
	} else {
		locks[newRegion].classHosts[host.HostClass].Lock()
		index := Sort(regions[newRegion].classHosts[host.HostClass], host.TotalResourcesUtilization)
		regions[newRegion].classHosts[host.HostClass] = InsertHost(regions[newRegion].classHosts[host.HostClass], index, host)
		locks[newRegion].classHosts[host.HostClass].Unlock()
	}
}

//used by initial scheduling and cut algorithm
func GetListHostsLEE_DEE(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	requestClass := params["requestclass"]
	listType := params["listtype"]

	listHosts := make([]*Host, 0)
	listHostsDEE := make([]*Host, 0)

	//1 for initial scheduling 2 for cut algorithm
	if listType == "1" {
		listHosts = GetHostsLEE_normal(requestClass)
		listHostsDEE = GetHostsDEE_normal(requestClass)

	} else {
		listHosts = GetHostsLEE_cut(requestClass)
		listHostsDEE = GetHostsDEE_cut(requestClass)

	}
	listHosts = append(listHosts, listHostsDEE...)
	fmt.Println("Got hosts")
	fmt.Println(listHosts)

	json.NewEncoder(w).Encode(listHosts)

}

//used by kill algorithm
func GetListHostsEED_DEE(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	requestClass := params["requestclass"]

	listHosts := GetHostsEED(requestClass)
	listHostsDEE := GetHostsDEE_kill(requestClass)

	listHosts = append(listHosts, listHostsDEE...)

	fmt.Println("Got kill zone hosts")
	json.NewEncoder(w).Encode(listHosts)

}

//for initial scheduling algorithm without resorting to cuts or kills
func GetHostsLEE_normal(requestClass string) []*Host {
	//we only get hosts that respect requestClass >= hostClass and order them by ascending order of their class
	//class 1 hosts are always selected

	listHosts := make([]*Host, 0)

	if requestClass == "1" {
		locks["LEE"].classHosts["1"].Lock()
		listHosts = append(listHosts, regions["LEE"].classHosts["1"]...)
		locks["LEE"].classHosts["1"].Unlock()
	} else if requestClass == "2" {
		locks["LEE"].classHosts["1"].Lock()
		listHosts = append(listHosts, regions["LEE"].classHosts["1"]...)
		locks["LEE"].classHosts["1"].Unlock()

		locks["LEE"].classHosts["2"].Lock()
		listHosts = append(listHosts, regions["LEE"].classHosts["2"]...)
		locks["LEE"].classHosts["2"].Unlock()

	} else if requestClass == "3" {
		locks["LEE"].classHosts["1"].Lock()
		listHosts = append(listHosts, regions["LEE"].classHosts["1"]...)
		locks["LEE"].classHosts["1"].Unlock()

		locks["LEE"].classHosts["2"].Lock()
		listHosts = append(listHosts, regions["LEE"].classHosts["2"]...)
		locks["LEE"].classHosts["2"].Unlock()

		locks["LEE"].classHosts["3"].Lock()
		listHosts = append(listHosts, regions["LEE"].classHosts["3"]...)
		locks["LEE"].classHosts["3"].Unlock()

	} else if requestClass == "4" {
		locks["LEE"].classHosts["1"].Lock()
			listHosts = append(listHosts, regions["LEE"].classHosts["1"]...)
		locks["LEE"].classHosts["1"].Unlock()

		locks["LEE"].classHosts["2"].Lock()
			listHosts = append(listHosts, regions["LEE"].classHosts["2"]...)
		locks["LEE"].classHosts["2"].Unlock()

		locks["LEE"].classHosts["3"].Lock()
			listHosts = append(listHosts, regions["LEE"].classHosts["3"]...)
		locks["LEE"].classHosts["3"].Unlock()

		locks["LEE"].classHosts["4"].Lock()

			listHosts = append(listHosts, regions["LEE"].classHosts["4"]...)

		locks["LEE"].classHosts["4"].Unlock()

	}
	return listHosts
}

//for CUT algorithm
func GetHostsLEE_cut(requestClass string) []*Host {
	//we only get hosts that respect requestClass <= hostClass and order them by ascending order of their class
	//class 1 hosts are always selected

	listHosts := make([]*Host, 0)

	if requestClass == "1" {
		locks["LEE"].classHosts["1"].Lock()
		listHosts = append(listHosts, regions["LEE"].classHosts["1"]...)
		locks["LEE"].classHosts["1"].Unlock()

		locks["LEE"].classHosts["2"].Lock()
		listHosts = append(listHosts, regions["LEE"].classHosts["2"]...)
		locks["LEE"].classHosts["2"].Unlock()
		
		locks["LEE"].classHosts["3"].Lock()
		listHosts = append(listHosts, regions["LEE"].classHosts["3"]...)
		locks["LEE"].classHosts["3"].Unlock()
		
		locks["LEE"].classHosts["4"].Lock()
		listHosts = append(listHosts, regions["LEE"].classHosts["4"]...)
		locks["LEE"].classHosts["4"].Unlock()

	} else if requestClass == "2" {
		locks["LEE"].classHosts["2"].Lock()
		listHosts = append(listHosts, regions["LEE"].classHosts["2"]...)
		locks["LEE"].classHosts["2"].Unlock()

		locks["LEE"].classHosts["3"].Lock()	
		listHosts = append(listHosts, regions["LEE"].classHosts["3"]...)
		locks["LEE"].classHosts["3"].Unlock()

		locks["LEE"].classHosts["4"].Lock()
		listHosts = append(listHosts, regions["LEE"].classHosts["4"]...)
		locks["LEE"].classHosts["4"].Unlock()
	} else if requestClass == "3" {
		locks["LEE"].classHosts["3"].Lock()
		listHosts = append(listHosts, regions["LEE"].classHosts["3"]...)
		locks["LEE"].classHosts["3"].Unlock()


		locks["LEE"].classHosts["4"].Lock()
		listHosts = append(listHosts, regions["LEE"].classHosts["4"]...)
		locks["LEE"].classHosts["4"].Unlock()

	} else if requestClass == "4" {
		locks["LEE"].classHosts["4"].Lock()
		listHosts = append(listHosts, regions["LEE"].classHosts["4"]...)
		locks["LEE"].classHosts["4"].Unlock()
	}

	return listHosts
}

//for initial scheduling algori
func GetHostsDEE_normal(requestClass string) []*Host {
	//we only get hosts that respect requestClass <= hostClass and order them by ascending order of their class
	//class 1 hosts are always selected
	listHosts := make([]*Host, 0)

	if requestClass == "1" {
		locks["DEE"].classHosts["1"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["1"]...)
		locks["DEE"].classHosts["1"].Unlock()

	} else if requestClass == "2" {
		locks["DEE"].classHosts["1"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["1"]...)
		locks["DEE"].classHosts["1"].Unlock()
		
		locks["DEE"].classHosts["2"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["2"]...)
		locks["DEE"].classHosts["2"].Unlock()
	} else if requestClass == "3" {
		locks["DEE"].classHosts["1"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["1"]...)
		locks["DEE"].classHosts["1"].Unlock()

		locks["DEE"].classHosts["2"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["2"]...)
		locks["DEE"].classHosts["2"].Unlock()

		locks["DEE"].classHosts["3"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["3"]...)
		locks["DEE"].classHosts["3"].Unlock()

	} else if requestClass == "4" {
		locks["DEE"].classHosts["1"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["1"]...)
		locks["DEE"].classHosts["1"].Unlock()

		locks["DEE"].classHosts["2"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["2"]...)
		locks["DEE"].classHosts["2"].Unlock()

		locks["DEE"].classHosts["3"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["3"]...)
		locks["DEE"].classHosts["3"].Unlock()

		locks["DEE"].classHosts["4"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["4"]...)
		locks["DEE"].classHosts["4"].Unlock()
	}
	return listHosts
}

//for CUT algorithm
func GetHostsDEE_cut(requestClass string) []*Host {
	//we only get hosts that respect requestClass <= hostClass and order them by ascending order of their class
	//class 1 hosts are always selected
	listHosts := make([]*Host, 0)
	if requestClass == "1" {
		locks["DEE"].classHosts["1"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["1"]...)
		locks["DEE"].classHosts["1"].Unlock()

		locks["DEE"].classHosts["2"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["2"]...)
		locks["DEE"].classHosts["2"].Unlock()

		locks["DEE"].classHosts["3"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["3"]...)
		locks["DEE"].classHosts["3"].Unlock()

		locks["DEE"].classHosts["4"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["4"]...)
		locks["DEE"].classHosts["4"].Unlock()

	} else if requestClass == "2" {
		locks["DEE"].classHosts["2"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["2"]...)
		locks["DEE"].classHosts["2"].Unlock()

		locks["DEE"].classHosts["3"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["3"]...)
		locks["DEE"].classHosts["3"].Unlock()

		locks["DEE"].classHosts["4"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["4"]...)
		locks["DEE"].classHosts["4"].Unlock()

	} else if requestClass == "3" {
		locks["DEE"].classHosts["3"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["3"]...)
		locks["DEE"].classHosts["3"].Unlock()

		locks["DEE"].classHosts["4"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["4"]...)
		locks["DEE"].classHosts["4"].Unlock()
	} else if requestClass == "4" {
		locks["DEE"].classHosts["4"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["4"]...)
		locks["DEE"].classHosts["4"].Unlock()
	}
	return listHosts
}

//for KILL algorithm
func GetHostsDEE_kill(requestClass string) []*Host {
	listHosts := make([]*Host, 0)

	switch requestClass {
	case "1":
		locks["DEE"].classHosts["1"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["1"]...)
		locks["DEE"].classHosts["1"].Unlock()

		locks["DEE"].classHosts["2"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["2"]...)
		locks["DEE"].classHosts["2"].Unlock()

		locks["DEE"].classHosts["3"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["3"]...)
		locks["DEE"].classHosts["3"].Unlock()

		locks["DEE"].classHosts["4"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["4"]...)
		locks["DEE"].classHosts["4"].Unlock()
		break
	case "2":
		locks["DEE"].classHosts["2"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["2"]...)
		locks["DEE"].classHosts["2"].Unlock()

		locks["DEE"].classHosts["3"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["3"]...)
		locks["DEE"].classHosts["3"].Unlock()

		locks["DEE"].classHosts["4"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["4"]...)
		locks["DEE"].classHosts["4"].Unlock()

		locks["DEE"].classHosts["1"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["1"]...)
		locks["DEE"].classHosts["1"].Unlock()

		break
	case "3":
		locks["DEE"].classHosts["3"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["3"]...)
		locks["DEE"].classHosts["3"].Unlock()
	
		locks["DEE"].classHosts["4"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["4"]...)
		locks["DEE"].classHosts["4"].Unlock()

		locks["DEE"].classHosts["2"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["2"]...)
		locks["DEE"].classHosts["2"].Unlock()

		locks["DEE"].classHosts["1"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["1"]...)
		locks["DEE"].classHosts["1"].Unlock()

		break
	case "4":
		locks["DEE"].classHosts["4"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["4"]...)
		locks["DEE"].classHosts["4"].Unlock()

		locks["DEE"].classHosts["3"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["3"]...)
		locks["DEE"].classHosts["3"].Unlock()

		locks["DEE"].classHosts["2"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["2"]...)
		locks["DEE"].classHosts["2"].Unlock()

		locks["DEE"].classHosts["1"].Lock()
		listHosts = append(listHosts, regions["DEE"].classHosts["1"]...)
		locks["DEE"].classHosts["1"].Unlock()

		break
	}
	return listHosts
}

func GetHostsEED(requestClass string) []*Host {
	listHosts := make([]*Host, 0)

	switch requestClass {
	case "1":
		locks["EED"].classHosts["1"].Lock()
		listHosts = append(listHosts, regions["EED"].classHosts["1"]...)
		locks["EED"].classHosts["1"].Unlock()

		locks["EED"].classHosts["2"].Lock()
		listHosts = append(listHosts, regions["EED"].classHosts["2"]...)
		locks["EED"].classHosts["2"].Unlock()

		locks["EED"].classHosts["3"].Lock()
		listHosts = append(listHosts, regions["EED"].classHosts["3"]...)
		locks["EED"].classHosts["3"].Unlock()

		locks["EED"].classHosts["4"].Lock()
		listHosts = append(listHosts, regions["EED"].classHosts["4"]...)
		locks["EED"].classHosts["4"].Unlock()

		break
	case "2":
		locks["EED"].classHosts["2"].Lock()
		listHosts = append(listHosts, regions["EED"].classHosts["2"]...)
		locks["EED"].classHosts["2"].Unlock()

		locks["EED"].classHosts["3"].Lock()
		listHosts = append(listHosts, regions["EED"].classHosts["3"]...)
		locks["EED"].classHosts["3"].Unlock()
	
		locks["EED"].classHosts["4"].Lock()
		listHosts = append(listHosts, regions["EED"].classHosts["4"]...)
		locks["EED"].classHosts["4"].Unlock()
		
		locks["EED"].classHosts["1"].Lock()
		listHosts = append(listHosts, regions["EED"].classHosts["1"]...)
		locks["EED"].classHosts["1"].Unlock()
		break
	case "3":
		locks["EED"].classHosts["3"].Lock()
		listHosts = append(listHosts, regions["EED"].classHosts["3"]...)
		locks["EED"].classHosts["3"].Unlock()

		locks["EED"].classHosts["4"].Lock()
		listHosts = append(listHosts, regions["EED"].classHosts["4"]...)
		locks["EED"].classHosts["4"].Unlock()

		locks["EED"].classHosts["2"].Lock()
		listHosts = append(listHosts, regions["EED"].classHosts["2"]...)
		locks["EED"].classHosts["2"].Unlock()

		locks["EED"].classHosts["1"].Lock()
		listHosts = append(listHosts, regions["EED"].classHosts["1"]...)
		locks["EED"].classHosts["1"].Unlock()

		break
	case "4":
		locks["EED"].classHosts["4"].Lock()
		listHosts = append(listHosts, regions["EED"].classHosts["4"]...)
		locks["EED"].classHosts["4"].Unlock()

		locks["EED"].classHosts["3"].Lock()
		listHosts = append(listHosts, regions["EED"].classHosts["3"]...)
		locks["EED"].classHosts["3"].Unlock()

		locks["EED"].classHosts["2"].Lock()
		listHosts = append(listHosts, regions["EED"].classHosts["2"]...)
		locks["EED"].classHosts["2"].Unlock()

		locks["EED"].classHosts["1"].Lock()
		listHosts = append(listHosts, regions["EED"].classHosts["1"]...)
		locks["EED"].classHosts["1"].Unlock()

		break
	}
	return listHosts
}

//updates both memory and cpu. message received from energy monitors. 
func UpdateBothResources(w http.ResponseWriter, req *http.Request) {
	//the host is going to be identified by the IP
	fmt.Println("Updating both")

	params := mux.Vars(req)
	hostIP := params["hostip"]
	cpuUpdate := params["cpu"]
	memoryUpdate := params["memory"]

	locks[hosts[hostIP].Region].classHosts[hosts[hostIP].HostClass].Lock()			
	hosts[hostIP].CPU_Utilization = cpuUpdate
	hosts[hostIP].MemoryUtilization = memoryUpdate
	locks[hosts[hostIP].Region].classHosts[hosts[hostIP].HostClass].Unlock()				

	go UpdateTotalResourcesUtilization(cpuUpdate, memoryUpdate, 1, hostIP)
}

//function whose job is to check whether the total resources should be updated or not.
func UpdateTotalResourcesUtilization(cpu string, memory string, updateType int, hostIP string){
	//this will be used in case there is no region change to avoid updating the host position in its current region if its total has not changed
	previousTotalResourceUtilization := hosts[hostIP].TotalResourcesUtilization
	afterTotalResourceUtilization := ""

	switch updateType {
		case 1:
			newCPU,_ := strconv.ParseFloat(cpu,64)
			newMemory, _ := strconv.ParseFloat(memory, 64)
			afterTotalResourceUtilization = strconv.FormatFloat(math.Max(newCPU, newMemory), 'f',-1, 64)

			locks[hosts[hostIP].Region].classHosts[hosts[hostIP].HostClass].Lock()					
			hosts[hostIP].TotalResourcesUtilization = afterTotalResourceUtilization
			locks[hosts[hostIP].Region].classHosts[hosts[hostIP].HostClass].Unlock()					
			break
		case 2:
			newCPU,_ := strconv.ParseFloat(cpu,64)
			memory,_ := strconv.ParseFloat(hosts[hostIP].MemoryUtilization, 64)
			afterTotalResourceUtilization = strconv.FormatFloat(math.Max(newCPU, memory), 'f',-1, 64)

			locks[hosts[hostIP].Region].classHosts[hosts[hostIP].HostClass].Lock()							
			hosts[hostIP].TotalResourcesUtilization = afterTotalResourceUtilization
			locks[hosts[hostIP].Region].classHosts[hosts[hostIP].HostClass].Unlock()					
			break
		case 3:
			newMemory, _ := strconv.ParseFloat(memory, 64)
			cpu,_ := strconv.ParseFloat(hosts[hostIP].CPU_Utilization, 64)
			afterTotalResourceUtilization = strconv.FormatFloat(math.Max(cpu, newMemory), 'f',-1, 64)

			locks[hosts[hostIP].Region].classHosts[hosts[hostIP].HostClass].Lock()							
			hosts[hostIP].TotalResourcesUtilization = afterTotalResourceUtilization
			locks[hosts[hostIP].Region].classHosts[hosts[hostIP].HostClass].Unlock()					
			break
	}
	//now we must check if the host region should be updated or not
	if !CheckIfRegionUpdate(hostIP) && afterTotalResourceUtilization != previousTotalResourceUtilization { //if an update to the host region is not required then we update this host position inside its region list
		hostRegion := hosts[hostIP].Region
		go UpdateHostRegionList(hostRegion, hostRegion, hosts[hostIP])		
	}
}

func CheckIfRegionUpdate(hostIP string) bool {
	locks[hosts[hostIP].Region].classHosts[hosts[hostIP].HostClass].Lock()					
	if hosts[hostIP].TotalResourcesUtilization < "0.5" { //LEE region
		if hosts[hostIP].Region != "LEE" { //if this is true then we must update this host region because it changed
			locks[hosts[hostIP].Region].classHosts[hosts[hostIP].HostClass].Unlock()					
			UpdateHostRegion(hostIP, "LEE")
			return true
		}
	} else if hosts[hostIP].TotalResourcesUtilization < "0.85" { //DEE region
		if hosts[hostIP].Region != "DEE" { //if this is true then we must update this host region because it changed
			locks[hosts[hostIP].Region].classHosts[hosts[hostIP].HostClass].Unlock()					
			UpdateHostRegion(hostIP, "DEE")
			return true
		}
	} else { //EED region
		if hosts[hostIP].Region != "EED" { //if this is true then we must update this host region because it changed
			locks[hosts[hostIP].Region].classHosts[hosts[hostIP].HostClass].Unlock()					
			UpdateHostRegion(hostIP, "EED")
			return true
		}
	}
	locks[hosts[hostIP].Region].classHosts[hosts[hostIP].HostClass].Unlock()					
	return false
}

//information received from monitor
func UpdateCPU(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	hostIP := params["hostip"]
	cpuUpdate := params["cpu"]

	fmt.Println("Updating cpu " + cpuUpdate)

	locks[hosts[hostIP].Region].classHosts[hosts[hostIP].HostClass].Lock()					
	hosts[hostIP].CPU_Utilization = cpuUpdate
	locks[hosts[hostIP].Region].classHosts[hosts[hostIP].HostClass].Unlock()
	
	go UpdateTotalResourcesUtilization(cpuUpdate, "", 2, hostIP)
		
}

//information received from monitor
func UpdateMemory(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	hostIP := params["hostip"]
	memoryUpdate := params["memory"]

	fmt.Println("Updating memory")

	locks[hosts[hostIP].Region].classHosts[hosts[hostIP].HostClass].Lock()					
	hosts[hostIP].MemoryUtilization = memoryUpdate
	locks[hosts[hostIP].Region].classHosts[hosts[hostIP].HostClass].Unlock()
					
	go UpdateTotalResourcesUtilization("", memoryUpdate, 3, hostIP)
}

//this function is responsible for receiving by the Scheduler the task that has ended and warn the task registry it no longer exists
//it will also reduce the amount of allocated resources on the host it used to run
var counter = 0

func WarnTaskRegistry(w http.ResponseWriter, req *http.Request){
	params := mux.Vars(req)
	taskID := params["taskid"]
	
	 //get IP from the host where the container is running

	if counter == 0 {
	counter = 1

    cmd := "docker"
    args := []string{"-H", "tcp://0.0.0.0:2376", "inspect", "--format", "{{ .Node.IP }}",taskID }

    var commandOutput []byte 
    var err error 

    if commandOutput,err = exec.Command(cmd, args...).Output(); err != nil {
    	fmt.Println("Error using docker run")
        fmt.Println(err)
   }
   	hostIP := string(commandOutput)
	fmt.Println("AQUI")	
	fmt.Println(hostIP)
	fmt.Println(hostIP[0])
	
	//this code alerts task registry that the task must be removed. This must return as response the amount of resources this task was consuming so 
	//it can be taken from the allocatedMemory/CPUs
//	req, err1 := http.NewRequest("GET", "http://"+hostIP+":1234/task/remove/"+taskID, nil)
	req, err1 := http.NewRequest("GET", "http://146.193.41.143:1234/task/remove/"+taskID, nil)
  
	req.Header.Set("X-Custom-Header", "myvalue")
	req.Header.Set("Content-Type", "application/json")

    client := &http.Client{}
    resp, err1 := client.Do(req)
    if err1 != nil {
    	panic(err1)
    }
	defer resp.Body.Close()

	var taskResources *TaskResources
	_ = json.NewDecoder(resp.Body).Decode(&taskResources)
	
	//update the amount of allocated resources of the host this task was running
	//TODO: os ... sao retornados pelo task registry no get acima
	go UpdateResources(taskResources.CPU, taskResources.Memory, hostIP)
	}
}

func UpdateResources(cpuUpdate float64, memoryUpdate float64, hostIP string) {
	auxHost := hosts[hostIP]

    locks[auxHost.Region].classHosts[auxHost.HostClass].Lock()
    
    hosts[hostIP].AllocatedMemory -= cpuUpdate
    hosts[hostIP].AllocatedCPUs -= memoryUpdate

	//update overbooking of this host
	cpuOverbooking := hosts[hostIP].AllocatedCPUs / hosts[hostIP].TotalCPUs
    memoryOverbooking := hosts[hostIP].AllocatedMemory / hosts[hostIP].TotalMemory

    hosts[hostIP].OverbookingFactor = math.Max(cpuOverbooking, memoryOverbooking)
	//TODO ver se é preciso ver se é preciso fazer update na region ou na posiçao dentro da propria classe

    locks[auxHost.Region].classHosts[auxHost.HostClass].Unlock()

}

//updates information about allocated resources and recalculates overbooking factor.
//this is information received from the Scheduler when it makes a scheduling decision
func UpdateAllocatedResourcesAndOverbooking(w http.ResponseWriter, req *http.Request) {
	//é preciso host id, cpu e memoria do request 
	params := mux.Vars(req)
	hostIP := params["hostip"]
	newCPU := params["cpu"]
	newMemory := params["memory"]
	taskID := params["taskid"]

	auxCPU,_ := strconv.ParseFloat(newCPU, 64)
	auxMemory,_ := strconv.ParseFloat(newMemory, 64)

	//we must update it because of docker swarm bug
	
	fmt.Println(newCPU)

	if newCPU != "0" {
		cmd := "docker"
	    	args := []string{"-H", "tcp://0.0.0.0:2376", "update", "-c", newCPU, taskID}

    		if err := exec.Command(cmd, args...).Run(); err != nil {
        		fmt.Println("Error using docker run")
        		fmt.Println(err)
	    	}
	}

	go UpdateResources(-auxCPU, -auxMemory, hostIP)
}


func main() {
	regions = make(map[string]Region)
	hosts = make(map[string]*Host)
	locks = make(map[string]Lock)	

	ServeSchedulerRequests()
}

func assignHosts(){
	/*hosts["0"] = &Host{HostID: "0", HostIP: "192.168.1.170", HostClass: "1", Region: "LEE", TotalMemory: 5000000, TotalCPUs: 50000000}
	hosts["2"] = &Host{HostID: "2", HostClass: "1", Region: "LEE", TotalMemory: 50000000000, TotalCPUs: 50000000000, TotalResourcesUtilization:"0.45"}
	hosts["3"] = &Host{HostID: "3", HostClass: "1", Region: "LEE", TotalMemory: 50000000000, TotalCPUs: 50000000000, TotalResourcesUtilization:"0.37"}
	hosts["4"] = &Host{HostID: "4", HostClass: "1", Region: "LEE", TotalMemory: 50000000000, TotalCPUs: 50000000000, TotalResourcesUtilization:"0.33"}
	hosts["5"] = &Host{HostID: "5", HostClass: "2", Region: "DEE", TotalMemory: 50000000000, TotalCPUs: 50000000000}
	hosts["7"] = &Host{HostID: "7", HostClass: "1", Region: "EED", TotalMemory: 50000000000, TotalCPUs: 50000000000}
	hosts["8"] = &Host{HostID: "8", HostClass: "1", Region: "DEE", TotalMemory: 50000000000, TotalCPUs: 50000000000}
	hosts["1"] = &Host{HostID: "1", HostClass: "2", Region: "LEE", TotalMemory: 5000000, TotalCPUs: 50000000}*/
}

func ServeSchedulerRequests() {
	router := mux.NewRouter()
	//assignHosts()


	lockClassLEE := make(map[string]*sync.Mutex)
	lockClassDEE := make(map[string]*sync.Mutex)
	lockClassEED := make(map[string]*sync.Mutex)

	lockClassLEE["1"] = &sync.Mutex{}
	lockClassLEE["2"] = &sync.Mutex{}
	lockClassLEE["3"] = &sync.Mutex{}
	lockClassLEE["4"] = &sync.Mutex{}

	lockClassDEE["1"] = &sync.Mutex{}
	lockClassDEE["2"] = &sync.Mutex{}
	lockClassDEE["3"] = &sync.Mutex{}
	lockClassDEE["4"] = &sync.Mutex{}

	lockClassEED["1"] = &sync.Mutex{}
	lockClassEED["2"] = &sync.Mutex{}
	lockClassEED["3"] = &sync.Mutex{}
	lockClassEED["4"] = &sync.Mutex{}

	locks["LEE"] = Lock{classHosts: lockClassLEE, lock: &sync.Mutex{}}
	locks["DEE"] = Lock{lockClassDEE, &sync.Mutex{}}
	locks["EED"] = Lock{lockClassEED, &sync.Mutex{}}


	classLEE := make(map[string][]*Host)
	classDEE := make(map[string][]*Host)
	classEED := make(map[string][]*Host)

/*	list1LEE := make([]*Host, 0)
	list1DEE := make([]*Host, 0)
	list1EED := make([]*Host, 0)
	list2LEE := make([]*Host, 0)
	list2DEE := make([]*Host, 0)
//	list3 := make([]*Host, 0)
//	list4 := make([]*Host, 0)

	list1LEE = append(list1LEE, hosts["2"])
	list1LEE = append(list1LEE, hosts["3"])
	list1LEE = append(list1LEE, hosts["4"])
	list1LEE = append(list1LEE, hosts["0"])

	//list2LEE = append(list2LEE, hosts["1"])
	//list3 = append(list3, hosts["2"])
	//list4 = append(list4, hosts["3"])
	list2DEE = append(list2DEE, hosts["5"])
	list1EED = append(list1EED, hosts["7"])
	list1DEE = append(list1DEE, hosts["8"])
	list2LEE = append(list2LEE, hosts["1"])

	classLEE["1"] = list1LEE
	classLEE["2"] = list2LEE
	//classLEE["3"] = list3
	//classLEE["4"] = list4
	classDEE["1"] = list1DEE
	classDEE["2"] = list2DEE
	classEED["1"] = list1EED
*/
	regions["LEE"] = Region{classLEE}
	regions["DEE"] = Region{classDEE}
	regions["EED"] = Region{classEED}

	//	router.HandleFunc("/host/{hostid}", GetHost).Methods("GET")
	router.HandleFunc("/host/list/{requestclass}&{listtype}", GetListHostsLEE_DEE).Methods("GET")
	router.HandleFunc("/host/listkill/{requestclass}", GetListHostsEED_DEE).Methods("GET")
	router.HandleFunc("/host/updateclass/{requestclass}&{hostip}", UpdateHostClass).Methods("GET")
//	router.HandleFunc("/host/createhost", CreateHost).Methods("POST")
	router.HandleFunc("/host/createhost/{hostip}&{totalmemory}&{totalcpu}", CreateHost).Methods("GET")
	router.HandleFunc("/host/addworker/{hostip}&{workerid}", AddWorker).Methods("POST")
	router.HandleFunc("/host/updatetask/{taskid}&{newcpu}&{newmemory}&{hostip}&{cpucut}&{memorycut}", UpdateTaskResources).Methods("GET")
	router.HandleFunc("/host/killtask/{taskid}&{taskcpu}&{taskmemory}&{hostip}", KillTasks).Methods("GET")
	router.HandleFunc("/host/reschedule/{cpu}&{memory}&{requestclass}&{image}", RescheduleTask).Methods("GET")
	router.HandleFunc("/host/updateboth/{hostip}&{cpu}&{memory}", UpdateBothResources).Methods("GET")
	router.HandleFunc("/host/updatecpu/{hostip}&{cpu}", UpdateCPU).Methods("GET")
	router.HandleFunc("/host/updatememory/{hostip}&{memory}", UpdateMemory).Methods("GET")
	router.HandleFunc("/host/updateresources/{hostip}&{cpu}&{memory}&{taskid}", UpdateAllocatedResourcesAndOverbooking).Methods("GET")
	router.HandleFunc("/host/deletetask/{taskid}", WarnTaskRegistry).Methods("GET")

	//	router.HandleFunc("/people/{id}", GetPersonEndpoint).Methods("GET")
	//	router.HandleFunc("/people/{id}", CreatePersonEndpoint).Methods("POST")
	//	router.HandleFunc("/people/{id}", DeletePersonEndpoint).Methods("DELETE")
	log.Fatal(http.ListenAndServe(getIPAddress()+":12345", router))
}

func getIPAddress() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		fmt.Println(err.Error())
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				fmt.Println(ipnet.IP.String())
				return ipnet.IP.String()
			}
		}
	}
	return ""
}

