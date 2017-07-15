package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os/exec"
	"sync"
	"math"
	"strconv"	
	"bytes"
	"fmt"
	"os"

	"github.com/gorilla/mux"
)

type Host struct {
	HostIP                    string       `json:"hostip, omitempty"`
	HostClass                 string       `json:"hostclass,omitempty"`
	Region                    string       `json:"region,omitempty"`
	TotalResourcesUtilization float64      `json:"totalresouces,omitempty"`
	CPU_Utilization           float64      `json:"cpu,omitempty"`
	MemoryUtilization         float64      `json:"memory,omitempty"`
	AllocatedMemory           int64        `json:"allocatedmemory,omitempty"`
	AllocatedCPUs             int64        `json:"allocatedcpus,omitempty"`
	OverbookingFactor         float64      `json:"overbookingfactor,omitempty"`
	TotalMemory		  int64	       `json:"totalmemory,omitempty"`
	TotalCPUs		  int64	       `json:"totalcpus, omitempty"`
}

type TaskResources struct {
	CPU			int64		`json:"cpu, omitempty"`
	Memory 			int64		`json:"memory,omitempty"`
	PreviousClass 		string		`json:"previousclass,omitempty"`
	NewClass 		string		`json:"newclass,omitempty"`
	Update 			bool		`json:"update,omitempty"`
	IP			string		`json:"ip,omitempty"`
}

//this struct is used when a rescheduling is performed
type Task struct {
	CPU 		string 	`json:"cpu, omitempty"`
	Memory 		string 	`json:"memory,omitempty"`
	TaskClass 	string	`json:"taskclass,omitempty"`
	Image 		string 	`json:"image,omitempty"`
	TaskType 	string  `json:"tasktype,omitempty"`
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
func Sort(classList []*Host, searchValue float64) int {
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
func ReverseSort(classList []*Host, searchValue float64) int {
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
	var task Task
	_ = json.NewDecoder(req.Body).Decode(&task)	

	cmd := exec.Command("docker","-H", "tcp://10.5.60.2:2377","run", "-itd", "-c", task.CPU, "-m", task.Memory, "-e", "affinity:requestclass==" + task.TaskClass, "-e", "affinity:requesttype==" + task.TaskType, "-e", "affinity:rescheduled==yes", task.Image)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		fmt.Println("Error using docker run at rescheduling")
		fmt.Println(fmt.Sprint(err) + ": " + stderr.String())
	}
}

func TaskTerminated(w http.ResponseWriter, req *http.Request) {
	var taskResources *TaskResources
	_ = json.NewDecoder(req.Body).Decode(&taskResources)

	hostIP := taskResources.IP

	hostRegion := hosts[hostIP].Region
	hostClass := hosts[hostIP].HostClass

	//update resources of this host. It will have less resources since a task has terminated
	UpdateResources(taskResources.CPU, taskResources.Memory, hostIP)

	//we must check if host class should be updated. Could be last task restraining host class (e.g. last  class 1 task)
	locks[hostRegion].classHosts[hostClass].Lock()
	if taskResources.Update && taskResources.PreviousClass == hostClass {
		locks[hostRegion].classHosts[hostClass].Unlock()
		go UpdateHostList(taskResources.PreviousClass, taskResources.NewClass, hosts[hostIP])	
	}else {
		locks[hostRegion].classHosts[hostClass].Unlock()
	}
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

	cmd := exec.Command("docker","-H", "tcp://10.5.60.2:2377","update", "-m", newMemory, "-c", newCPU, taskID)
        var out, stderr bytes.Buffer
        cmd.Stdout = &out
        cmd.Stderr = &stderr

        if err := cmd.Run(); err != nil {
                fmt.Println("Error using docker run at update task resources after a cut")
                fmt.Println(fmt.Sprint(err) + ": " + stderr.String())
        }

	//now to update the resources of the host. Because of the cut, less resources will be occupied on the host
		
	memoryReduction, _ := strconv.ParseInt(memoryCut,10,64)
	cpuReduction, _ := strconv.ParseInt(cpuCut,10,64)
	hostRegion := hosts[hostIP].Region
	hostClass := hosts[hostIP].HostClass

	locks[hostRegion].classHosts[hostClass].Lock()
  
    	hosts[hostIP].AllocatedMemory -= memoryReduction
    	hosts[hostIP].AllocatedCPUs -= cpuReduction

    	locks[hostRegion].classHosts[hostClass].Unlock()
}

func CreateHost(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	hostIP := params["hostip"]
	totalMemory,_ := strconv.ParseInt(params["totalmemory"],10,64)
	totalCPUs,_ := strconv.ParseInt(params["totalcpu"],10,64) 
	totalCPUs *= 1024 // *1024 because 1024 shares equals using 1 cpu by 100%	

	//since a host is created it will not have tasks assigned to it so it goes to the LEE region to the less restrictive class
	
	locks["LEE"].classHosts["4"].Lock()
	hosts[hostIP] = &Host{HostIP: hostIP, HostClass: "4", Region: "LEE", TotalMemory: totalMemory, TotalCPUs: totalCPUs, AllocatedMemory: 0, AllocatedCPUs: 0,
	TotalResourcesUtilization: 0.0, CPU_Utilization: 0.0, MemoryUtilization: 0.0, OverbookingFactor:0.0}
	
	newHost := make([]*Host, 0)
	newHost = append(newHost, hosts[hostIP])
	
	regions["LEE"].classHosts["4"] = append(regions["LEE"].classHosts["4"], newHost...)
	locks["LEE"].classHosts["4"].Unlock()

}

//function used to update host class when a new task arrives
//implies list change
func UpdateHostClass(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	newHostClass := params["requestclass"]
	hostIP := params["hostip"]

	currentClass := hosts[hostIP].HostClass
	hostRegion := hosts[hostIP].Region

	locks[hostRegion].classHosts[currentClass].Lock()

	if currentClass > newHostClass { //we only update the host class if the current class is higher
		locks[hostRegion].classHosts[currentClass].Unlock()
		//we need to update the list where this host is at
		UpdateHostList(currentClass, newHostClass, hosts[hostIP])
		return
	}
	locks[hostRegion].classHosts[currentClass].Unlock()
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
	hostRegion := host.Region
	//this deletes
	locks[hostRegion].classHosts[hostPreviousClass].Lock()
	for i := 0; i < len(regions[hostRegion].classHosts[hostPreviousClass]); i++ {
		if regions[hostRegion].classHosts[hostPreviousClass][i].HostIP == host.HostIP {
			regions[hostRegion].classHosts[hostPreviousClass] = append(regions[hostRegion].classHosts[hostPreviousClass][:i], regions[hostRegion].classHosts[hostPreviousClass][i+1:]...)
			break
		}
	}
	
	locks[hostRegion].classHosts[hostPreviousClass].Unlock()
		
	locks[hostRegion].classHosts[hostNewClass].Lock()
	//this inserts in new list
	if hostRegion == "LEE" || hostRegion == "DEE" {
		index := ReverseSort(regions[hostRegion].classHosts[hostNewClass], host.TotalResourcesUtilization)
		regions[hostRegion].classHosts[hostNewClass] = InsertHost(regions[hostRegion].classHosts[hostNewClass], index, host)
	} else {
		index := Sort(regions[hostRegion].classHosts[hostNewClass], host.TotalResourcesUtilization)
		regions[hostRegion].classHosts[hostNewClass] = InsertHost(regions[hostRegion].classHosts[hostNewClass], index, host)
	}
	hosts[host.HostIP].HostClass = hostNewClass
	locks[hostRegion].classHosts[hostNewClass].Unlock()
}

//implies list change
func UpdateHostRegion(hostIP string, newRegion string) {
	
	hostRegion := hosts[hostIP].Region
	hostClass := hosts[hostIP].HostClass

	locks[hostRegion].classHosts[hostClass].Lock()
	oldRegion := hostRegion
	
	locks[oldRegion].classHosts[hosts[hostIP].HostClass].Unlock()

	UpdateHostRegionList(oldRegion, newRegion, hosts[hostIP])
	return
}

//first we must remove the host from the previous region then insert it in the new onw
func UpdateHostRegionList(oldRegion string, newRegion string, host *Host) {
	
	hostClass := host.HostClass
	//this deletes
	locks[oldRegion].classHosts[hostClass].Lock()

	for i := 0; i < len(regions[oldRegion].classHosts[hostClass]); i++ {
		if regions[oldRegion].classHosts[hostClass][i].HostIP == host.HostIP {
			regions[oldRegion].classHosts[hostClass] = append(regions[oldRegion].classHosts[hostClass][:i], regions[oldRegion].classHosts[hostClass][i+1:]...)
			break
		}
	}
	locks[oldRegion].classHosts[hostClass].Unlock()
	locks[newRegion].classHosts[hostClass].Lock()
			
	//this inserts in new list
	if newRegion == "LEE" || newRegion == "DEE" {
		index := ReverseSort(regions[newRegion].classHosts[hostClass], host.TotalResourcesUtilization)		
		regions[newRegion].classHosts[hostClass] = InsertHost(regions[newRegion].classHosts[hostClass], index, host)
	} else {
		index := Sort(regions[newRegion].classHosts[hostClass], host.TotalResourcesUtilization)
		regions[newRegion].classHosts[hostClass] = InsertHost(regions[newRegion].classHosts[hostClass], index, host)
	}

	hosts[host.HostIP].Region = newRegion
	locks[newRegion].classHosts[hostClass].Unlock()
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
	fmt.Print("Getting hosts LEE and DEE")
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
	fmt.Print("Getting hosts EED and DEE")
	fmt.Println(listHosts)

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
	//we get all the hosts because the incoming request could fit in any if it receives a cut. However we only check tasks to cut where requestClass <= hostClass
	//because at the other hosts there won't be probably anything we can cut so its not waste to cost of searching them.

	listHosts := make([]*Host, 0)

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

	return listHosts
}

//for initial scheduling algori
func GetHostsDEE_normal(requestClass string) []*Host {
	//we only get hosts that respect requestClass >= hostClass and order them by ascending order of their class
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
	//we get all the hosts because the incoming request could fit in any if it receives a cut. However we only check tasks to cut where requestClass <= hostClass
	//because at the other hosts there won't be probably anything we can cut so its not waste to cost of searching them.

	listHosts := make([]*Host, 0)
	
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

	params := mux.Vars(req)
	hostIP := params["hostip"]
	cpuUpdate := params["cpu"]
	memoryUpdate := params["memory"]
	
	cpuToUpdate, _ := strconv.ParseFloat(cpuUpdate,64)
	memoryToUpdate, _ := strconv.ParseFloat(memoryUpdate,64)

	hostRegion := hosts[hostIP].Region
	hostClass := hosts[hostIP].HostClass

	locks[hostRegion].classHosts[hostClass].Lock()			
	hosts[hostIP].CPU_Utilization = cpuToUpdate
	hosts[hostIP].MemoryUtilization = memoryToUpdate
	locks[hostRegion].classHosts[hostClass].Unlock()				

	go UpdateTotalResourcesUtilization(cpuToUpdate, memoryToUpdate, 1, hostIP)
}

//benchmark: gathers data regarding cpu and memory utilization of host for post analysis
func GatherData(cpu float64, memory float64, hostIP string) {
	//write the data gathered to a file
	// open files r and w
	cpuUtilization := strconv.FormatFloat(cpu,'f', -1, 64)
	memoryUtilization := strconv.FormatFloat(memory,'f', -1, 64)

     	fileCPU, err1 := os.OpenFile(hostIP+"cpu.txt", os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
     	fileMemory, err2 := os.OpenFile(hostIP+"memory.txt", os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
     	if err1 != nil || err2 != nil{
       		panic(err1)
     	}
        defer fileCPU.Close()
        defer fileMemory.Close()

     	if _, err1 = fileCPU.WriteString(cpuUtilization); err1 != nil {
      		panic(err1)
     	}
     	if _, err2 = fileMemory.WriteString(memoryUtilization); err2 != nil {
      		panic(err2)
     	}

}

//function whose job is to check whether the total resources should be updated or not.
func UpdateTotalResourcesUtilization(cpu float64, memory float64, updateType int, hostIP string){
	//this will be used in case there is no region change to avoid updating the host position in its current region if its total has not changed
	previousTotalResourceUtilization := hosts[hostIP].TotalResourcesUtilization
	afterTotalResourceUtilization := 0.0

	hostRegion := hosts[hostIP].Region
	hostClass := hosts[hostIP].HostClass

	//benchmark purposes, gathering data
	locks[hostRegion].classHosts[hostClass].Lock()
        GatherData(hosts[hostIP].CPU_Utilization, hosts[hostIP].MemoryUtilization, hostIP)
        locks[hostRegion].classHosts[hostClass].Unlock()


	//1-> both resources, 2-> cpu, 3-> memory
	switch updateType {
		case 1:
			afterTotalResourceUtilization = math.Max(cpu, memory)

			locks[hostRegion].classHosts[hostClass].Lock()					
			hosts[hostIP].TotalResourcesUtilization = afterTotalResourceUtilization
			locks[hostRegion].classHosts[hostClass].Unlock()					
			break
		case 2:
			memoryCurrent := hosts[hostIP].MemoryUtilization
			afterTotalResourceUtilization = math.Max(cpu, memoryCurrent)

			locks[hostRegion].classHosts[hostClass].Lock()							
			hosts[hostIP].TotalResourcesUtilization = afterTotalResourceUtilization
			locks[hostRegion].classHosts[hostClass].Unlock()					
			break
		case 3:
			cpuCurrent := hosts[hostIP].CPU_Utilization
			afterTotalResourceUtilization = math.Max(cpuCurrent, memory)

			locks[hostRegion].classHosts[hostClass].Lock()							
			hosts[hostIP].TotalResourcesUtilization = afterTotalResourceUtilization
			locks[hostRegion].classHosts[hostClass].Unlock()					
			break
	}
	//now we must check if the host region should be updated or not
	if !CheckIfRegionUpdate(hostIP) && afterTotalResourceUtilization != previousTotalResourceUtilization { //if an update to the host region is not required then we update this host position inside its region list
		hostRegion := hosts[hostIP].Region
		go UpdateHostRegionList(hostRegion, hostRegion, hosts[hostIP])		
	}
}

func CheckIfRegionUpdate(hostIP string) bool {
	hostRegion := hosts[hostIP].Region
	hostClass := hosts[hostIP].HostClass

	locks[hostRegion].classHosts[hostClass].Lock()					
	if hosts[hostIP].TotalResourcesUtilization < 0.5 { //LEE region
		if hostRegion != "LEE" { //if this is true then we must update this host region because it changed
			locks[hostRegion].classHosts[hostClass].Unlock()					
			UpdateHostRegion(hostIP, "LEE")
			return true
		}
	} else if hosts[hostIP].TotalResourcesUtilization < 0.85 { //DEE region
		if hostRegion != "DEE" { //if this is true then we must update this host region because it changed
			locks[hostRegion].classHosts[hostClass].Unlock()					
			UpdateHostRegion(hostIP, "DEE")
			return true
		}
	} else { //EED region
		if hostRegion != "EED" { //if this is true then we must update this host region because it changed
			locks[hostRegion].classHosts[hostClass].Unlock()					
			UpdateHostRegion(hostIP, "EED")
			return true
		}
	}
	locks[hostRegion].classHosts[hostClass].Unlock()					
	return false
}

//information received from monitor
func UpdateCPU(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	hostIP := params["hostip"]
	cpuUpdate := params["cpu"]

	cpuToUpdate, _ := strconv.ParseFloat(cpuUpdate,64)

	hostRegion := hosts[hostIP].Region
	hostClass := hosts[hostIP].HostClass

	locks[hostRegion].classHosts[hostClass].Lock()					
	hosts[hostIP].CPU_Utilization = cpuToUpdate
	locks[hostRegion].classHosts[hostClass].Unlock()
	
	go UpdateTotalResourcesUtilization(cpuToUpdate, 0.0, 2, hostIP)
		
}

//information received from monitor
func UpdateMemory(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	hostIP := params["hostip"]
	memoryUpdate := params["memory"]

	memoryToUpdate, _ := strconv.ParseFloat(memoryUpdate,64)

	hostRegion := hosts[hostIP].Region
	hostClass := hosts[hostIP].HostClass

	locks[hostRegion].classHosts[hostClass].Lock()					
	hosts[hostIP].MemoryUtilization = memoryToUpdate
	locks[hostRegion].classHosts[hostClass].Unlock()
					
	go UpdateTotalResourcesUtilization(0.0, memoryToUpdate, 3, hostIP)
}

func UpdateResources(cpuUpdate int64, memoryUpdate int64, hostIP string) {
	hostRegion := hosts[hostIP].Region
	hostClass := hosts[hostIP].HostClass

	locks[hostRegion].classHosts[hostClass].Lock()
    
    	hosts[hostIP].AllocatedMemory -= memoryUpdate
    	hosts[hostIP].AllocatedCPUs -= cpuUpdate

	//update overbooking of this host
	cpuOverbooking := float64(hosts[hostIP].AllocatedCPUs) / float64(hosts[hostIP].TotalCPUs)
    	memoryOverbooking := float64(hosts[hostIP].AllocatedMemory) / float64(hosts[hostIP].TotalMemory)

    	hosts[hostIP].OverbookingFactor = math.Max(cpuOverbooking, memoryOverbooking)
    	locks[hostRegion].classHosts[hostClass].Unlock()
}

//updates information about allocated resources and recalculates overbooking factor.
//this is information received from the Scheduler when it makes a scheduling decision
func UpdateAllocatedResourcesAndOverbooking(w http.ResponseWriter, req *http.Request) {
	//é preciso host id, cpu e memoria do request 
	params := mux.Vars(req)
	hostIP := params["hostip"]
	newCPU := params["cpu"]
	newMemory := params["memory"]

	auxCPU,_ := strconv.ParseInt(newCPU,10, 64)
	auxMemory,_ := strconv.ParseInt(newMemory,10, 64)

	go UpdateResources(-auxCPU, -auxMemory, hostIP)
}


func main() {
	regions = make(map[string]Region)
	hosts = make(map[string]*Host)
	locks = make(map[string]Lock)	

	ServeSchedulerRequests()
}

func ServeSchedulerRequests() {
	router := mux.NewRouter()

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

	regions["LEE"] = Region{classLEE}
	regions["DEE"] = Region{classDEE}
	regions["EED"] = Region{classEED}

	router.HandleFunc("/host/list/{requestclass}&{listtype}", GetListHostsLEE_DEE).Methods("GET")
	router.HandleFunc("/host/listkill/{requestclass}", GetListHostsEED_DEE).Methods("GET")
	router.HandleFunc("/host/updateclass/{requestclass}&{hostip}", UpdateHostClass).Methods("GET")
	router.HandleFunc("/host/createhost/{hostip}&{totalmemory}&{totalcpu}", CreateHost).Methods("GET")
	router.HandleFunc("/host/updatetask/{taskid}&{newcpu}&{newmemory}&{hostip}&{cpucut}&{memorycut}", UpdateTaskResources).Methods("GET")
	router.HandleFunc("/host/killtask", TaskTerminated).Methods("POST")
	router.HandleFunc("/host/reschedule", RescheduleTask).Methods("POST")
	router.HandleFunc("/host/updateboth/{hostip}&{cpu}&{memory}", UpdateBothResources).Methods("GET")
	router.HandleFunc("/host/updatecpu/{hostip}&{cpu}", UpdateCPU).Methods("GET")
	router.HandleFunc("/host/updatememory/{hostip}&{memory}", UpdateMemory).Methods("GET")
	router.HandleFunc("/host/updateresources/{hostip}&{cpu}&{memory}", UpdateAllocatedResourcesAndOverbooking).Methods("GET")

	log.Fatal(http.ListenAndServe(getIPAddress()+":12345", router))
}

func getIPAddress() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		fmt.Println(err.Error())
	}
	count := 0
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil && count == 1{
				fmt.Println(ipnet.IP.String())
				return ipnet.IP.String()
			}
			count++
		}
	}
	return ""
}

