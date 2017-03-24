package main

import (
	"encoding/json"
	"log"
	"fmt"
	"sync"
	"net/http"
	"github.com/gorilla/mux"
	"github.com/docker/swarm/scheduler/node"
	"os/exec"
	"time"
)
type Host struct {
        HostID      string              	`json:"hostid,omitempty"`
        WorkerNodesID []string          	`json:"workernodesid,omitempty"`
        WorkerNodes []*node.Node			`json:"workernodes,omitempty"`
		HostClass   string              	`json:"hostclass,omitempty"`
        Region      string              	`json:"region,omitempty"`
        TotalResourcesUtilization string   	`json:"totalresouces,omitempty"`
        CPU_Utilization int            		`json:"cpu,omitempty"`
        MemoryUtilization int          		`json:"memory,omitempty"`
		AvailableMemory int64				`json:"availablememory,omitempty"`
		AvailableCPUs int64					`json:"availablecpus,omitempty"`
        AllocatedResources int        	    `json:"resoucesallocated,omitempty"`
        TotalHostResources int         	    `json:"totalresources,omitempty"`
        OverbookingFactor int       	    `json:"overbookingfactor,omitempty"`
}


//Each region will have 4 lists, one for each overbooking class
//LEE=Lowest Energy Efficiency, DEE =Desired Energy Efficiency EED=Energy Efficiency Degradation
type Region struct {
	classHosts map[string][]*Host
}

var regions map[string]Region

var hosts []Host

var lockRegionLEE = &sync.Mutex{}
var lockRegionDEE = &sync.Mutex{}
var lockRegionEED = &sync.Mutex{}
var lockHosts = &sync.Mutex{}


//adapted binary search algorithm for inserting orderly based on total resources of a host
//this is ascending order (for EED region)
func Sort(classList []*Host, searchValue string)(int) {
    listLength := len(classList)
    lowerBound := 0
    upperBound := listLength- 1

    for {
        midPoint := (upperBound + lowerBound)/2

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
func ReverseSort(classList []*Host, searchValue string)(int) {
	listLength := len(classList)
    lowerBound := 0
    upperBound := listLength- 1

    for {
        midPoint := (upperBound + lowerBound)/2

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
    args := []string{"run","-itd", "-c", cpu,"-m",memory, "-e", "affinity:requestclass=="+requestClass, "--name", "lala1", image}

    if err := exec.Command(cmd, args...).Run(); err != nil {
        fmt.Println("Error using docker run")
        fmt.Println(err)
    }
}

func KillTasks(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	taskID := params["taskid"]

	cmd := "docker"
    args := []string{"kill",  taskID}

    if err := exec.Command(cmd, args...).Run(); err != nil {
        fmt.Println("Error using docker update")
        fmt.Println(err)
    }
}

//function responsible to update task resources when there's a cut
func UpdateTaskResources(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	taskID := params["taskid"]
	newCPU := params["newcpu"]
	newMemory := params["newmemory"]

	time.Sleep(time.Second * 2)

    //update the task with cut resources
    cmd := "docker"
    args := []string{"update", "-m", newMemory, "-c", newCPU, taskID}

    if err := exec.Command(cmd, args...).Run(); err != nil {
        fmt.Println("Error using docker update")
        fmt.Println(err)
    }
}

func CreateHost(w http.ResponseWriter, req *http.Request) {
	var host Host
	_ = json.NewDecoder(req.Body).Decode(&host)
	
	//since a host is created it will not have tasks assigned to it so it goes to the LEE region
	hosts = append(hosts, host)
	
	newHost := make([]*Host,0)
	newHost = append(newHost, &hosts[len(hosts)-1])

	regions["LEE"].classHosts["1"] = append(regions["LEE"].classHosts["1"],newHost...)

	fmt.Println(regions["LEE"].classHosts["1"])	
}

//function used to associate a worker to a host when the worker is created
func AddWorker(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
//	hostID := params["hostid"]
	workerID := params["workerid"]
/*
	for index, host := range hosts {
		if host.HostID == hostID {
			lockHosts.Lock()
			hosts[index].WorkerNodesID = append(hosts[index].WorkerNodesID, workerID) 
			lockHosts.Unlock()
			//TODO: por return aqui
		}
	}*/ 

	//PARA UMA FASE DE TESTES
	for index, _ := range hosts {
		lockHosts.Lock()
		hosts[index].WorkerNodesID = append(hosts[index].WorkerNodesID, workerID) 
		lockHosts.Unlock()
	}
}
	

//function used to update host class when a new task arrives
//implies list change
func UpdateHostClass(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	newHostClass := params["requestclass"]
	hostID := params["hostid"]
	
	for index, host := range hosts {
		if host.HostID == hostID && host.HostClass > newHostClass { //we only update the host class if the current class is higher
			hosts[index].HostClass = newHostClass
			//we need to update the list where this host is at
			UpdateHostList(host.HostClass, newHostClass, &hosts[index])
			return
		}
	}
}

func InsertHost(classHosts []*Host, index int, host *Host) ([]*Host) {
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
	for i := 0 ; i < len(regions[host.Region].classHosts[hostPreviousClass]); i++ {
		if 	regions[host.Region].classHosts[hostPreviousClass][i].HostID == host.HostID {
			regions[host.Region].classHosts[hostPreviousClass] = append(regions[host.Region].classHosts[hostPreviousClass][:i], regions[host.Region].classHosts[hostPreviousClass][i+1:]...)
		}
	}
	//this inserts in new list
	if host.Region == "LEE" || host.Region == "DEE" {
		index := ReverseSort(regions[host.Region].classHosts[hostNewClass], host.TotalResourcesUtilization)
		regions[host.Region].classHosts[hostNewClass] = InsertHost(regions[host.Region].classHosts[hostNewClass], index, host)
	} else {
		index := ReverseSort(regions[host.Region].classHosts[hostNewClass], host.TotalResourcesUtilization)
		regions[host.Region].classHosts[hostNewClass] = InsertHost(regions[host.Region].classHosts[hostNewClass], index, host)
	}
}


//implies list change
func UpdateHostRegion(hostID string, newRegion string) {
	for index, host := range hosts {
		if host.HostID == hostID {
			lockHosts.Lock()
			hosts[index].Region = newRegion
			UpdateHostRegionList(host.Region, newRegion, &hosts[index])
			lockHosts.Unlock()
			return 
		}
	}
}

//first we must remove the host from the previous region then insert it in the new onw
func UpdateHostRegionList(oldRegion string, newRegion string, host *Host) {
	//this deletes
	for i := 0 ; i < len(regions[oldRegion].classHosts[host.HostClass]); i++ {
		if 	regions[oldRegion].classHosts[host.HostClass][i].HostID == host.HostID {
			regions[oldRegion].classHosts[host.HostClass] = append(regions[oldRegion].classHosts[host.HostClass][:i], regions[oldRegion].classHosts[host.HostClass][i+1:]...)
		}
	}
	//this inserts in new list
	if newRegion  == "LEE" || host.Region == "DEE" {
		index := ReverseSort(regions[newRegion].classHosts[host.HostClass], host.TotalResourcesUtilization)
		regions[newRegion].classHosts[host.HostClass] = InsertHost(regions[newRegion].classHosts[host.HostClass], index, host)
	} else {
		index := ReverseSort(regions[newRegion].classHosts[host.HostClass], host.TotalResourcesUtilization)
		regions[newRegion].classHosts[host.HostClass] = InsertHost(regions[newRegion].classHosts[host.HostClass], index, host)
	}

}


//used by initial scheduling and cut algorithm
func GetListHostsLEE_DEE(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	requestClass := params["requestclass"]	
	listType := params["listtype"]

	listHosts := make([]*Host,0)
	listHostsDEE := make([]*Host,0)	
	
	//1 for initial scheduling 2 for cut algorithm
	if listType == "1" {
		listHosts = GetHostsLEE_normal(requestClass)
		listHostsDEE = GetHostsDEE_normal(requestClass)
 
	} else {
		listHosts = GetHostsLEE_cut(requestClass)
		listHostsDEE = GetHostsDEE_cut(requestClass)

	}
	listHosts = append(listHosts, listHostsDEE...)
	
	json.NewEncoder(w).Encode(listHosts)
	
}

//used by kill algorithm
func GetListHostsEED_DEE(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	requestClass := params["requestclass"]
	
	listHosts := GetHostsEED(requestClass)
	listHostsDEE := GetHostsDEE_kill(requestClass)
	
	listHosts = append(listHosts, listHostsDEE...)
	
	json.NewEncoder(w).Encode(listHosts)

}

//for initial scheduling algorithm without resorting to cuts or kills
func GetHostsLEE_normal(requestClass string) ([]*Host) {
	//we only get hosts that respect requestClass >= hostClass and order them by ascending order of their class 
	//class 1 hosts are always selected
	
	listHosts := make([]*Host,0)
	
	if requestClass == "1" {
		listHosts = append(listHosts, regions["LEE"].classHosts["1"]...)
	} else if requestClass == "2" {
		listHosts = append(listHosts, regions["LEE"].classHosts["1"]...)
		listHosts = append(listHosts, regions["LEE"].classHosts["2"]...)
	} else if requestClass == "3" {
		listHosts = append(listHosts, regions["LEE"].classHosts["1"]...)
		listHosts = append(listHosts, regions["LEE"].classHosts["2"]...)
		listHosts = append(listHosts, regions["LEE"].classHosts["3"]...)
	} else if requestClass == "4" {
		listHosts = append(listHosts, regions["LEE"].classHosts["1"]...)
		listHosts = append(listHosts, regions["LEE"].classHosts["2"]...)
		listHosts = append(listHosts, regions["LEE"].classHosts["3"]...)
		listHosts = append(listHosts, regions["LEE"].classHosts["4"]...)
	}
	return listHosts
}

//for CUT algorithm
func GetHostsLEE_cut(requestClass string) ([]*Host) {
	//we only get hosts that respect requestClass <= hostClass and order them by ascending order of their class 
	//class 1 hosts are always selected

	listHosts := make([]*Host,0)

	if requestClass == "1" {
		listHosts = append(listHosts, regions["LEE"].classHosts["1"]...)
		listHosts = append(listHosts, regions["LEE"].classHosts["2"]...)
		listHosts = append(listHosts, regions["LEE"].classHosts["3"]...)
		listHosts = append(listHosts, regions["LEE"].classHosts["4"]...)
	} else if requestClass == "2" {
		listHosts = append(listHosts, regions["LEE"].classHosts["2"]...)
		listHosts = append(listHosts, regions["LEE"].classHosts["3"]...)
		listHosts = append(listHosts, regions["LEE"].classHosts["4"]...)

	} else if requestClass == "3" {
		listHosts = append(listHosts, regions["LEE"].classHosts["3"]...)
		listHosts = append(listHosts, regions["LEE"].classHosts["4"]...)
	} else if requestClass == "4" {
		listHosts = append(listHosts, regions["LEE"].classHosts["4"]...)
	}

	return listHosts	
}

//for initial scheduling algori
func GetHostsDEE_normal(requestClass string) ([]*Host) {
	//we only get hosts that respect requestClass <= hostClass and order them by ascending order of their class 
	//class 1 hosts are always selected
	listHosts := make([]*Host,0)
	
	if requestClass == "1" {
		listHosts = append(listHosts, regions["DEE"].classHosts["1"]...)
	} else if requestClass == "2" {
		listHosts = append(listHosts, regions["DEE"].classHosts["1"]...)
		listHosts = append(listHosts, regions["DEE"].classHosts["2"]...)
	} else if requestClass == "3" {
		listHosts = append(listHosts, regions["DEE"].classHosts["1"]...)
		listHosts = append(listHosts, regions["DEE"].classHosts["2"]...)
		listHosts = append(listHosts, regions["DEE"].classHosts["3"]...)
	} else if requestClass == "4" {
		listHosts = append(listHosts, regions["DEE"].classHosts["1"]...)
		listHosts = append(listHosts, regions["DEE"].classHosts["2"]...)
		listHosts = append(listHosts, regions["DEE"].classHosts["3"]...)
		listHosts = append(listHosts, regions["DEE"].classHosts["4"]...)
	}

	return listHosts
}

//for CUT algorithm
func GetHostsDEE_cut(requestClass string) ([]*Host) {
	//we only get hosts that respect requestClass <= hostClass and order them by ascending order of their class 
	//class 1 hosts are always selected
	listHosts := make([]*Host,0)
	if requestClass == "1" {
		listHosts = append(listHosts, regions["DEE"].classHosts["1"]...)
		listHosts = append(listHosts, regions["DEE"].classHosts["2"]...)
		listHosts = append(listHosts, regions["DEE"].classHosts["3"]...)
		listHosts = append(listHosts, regions["DEE"].classHosts["4"]...)
	} else if requestClass == "2" {
		listHosts = append(listHosts, regions["DEE"].classHosts["2"]...)
		listHosts = append(listHosts, regions["DEE"].classHosts["3"]...)
		listHosts = append(listHosts, regions["DEE"].classHosts["4"]...)

	} else if requestClass == "3" {
		listHosts = append(listHosts, regions["DEE"].classHosts["3"]...)
		listHosts = append(listHosts, regions["DEE"].classHosts["4"]...)
	} else if requestClass == "4" {
		listHosts = append(listHosts, regions["DEE"].classHosts["4"]...)
	}

	return listHosts
	
}

//for KILL algorithm
func GetHostsDEE_kill(requestClass string) ([]*Host) {
	listHosts := make([]*Host,0)

	switch requestClass {
		case "1":
			listHosts = append(listHosts, regions["DEE"].classHosts["1"]...)
			listHosts = append(listHosts, regions["DEE"].classHosts["2"]...)
			listHosts = append(listHosts, regions["DEE"].classHosts["3"]...)
			listHosts = append(listHosts, regions["DEE"].classHosts["4"]...)
			break
		case "2":
			listHosts = append(listHosts, regions["DEE"].classHosts["2"]...)
			listHosts = append(listHosts, regions["DEE"].classHosts["3"]...)
			listHosts = append(listHosts, regions["DEE"].classHosts["4"]...)
			listHosts = append(listHosts, regions["DEE"].classHosts["1"]...)
			break
		case "3":
			listHosts = append(listHosts, regions["DEE"].classHosts["3"]...)
			listHosts = append(listHosts, regions["DEE"].classHosts["4"]...)
			listHosts = append(listHosts, regions["DEE"].classHosts["2"]...)
			listHosts = append(listHosts, regions["DEE"].classHosts["1"]...)
			break
		case "4":
			listHosts = append(listHosts, regions["DEE"].classHosts["4"]...)
			listHosts = append(listHosts, regions["DEE"].classHosts["3"]...)
			listHosts = append(listHosts, regions["DEE"].classHosts["2"]...)
			listHosts = append(listHosts, regions["DEE"].classHosts["1"]...)
			break
	}
	return listHosts
}

func GetHostsEED(requestClass string) ([]*Host) {
	listHosts := make([]*Host,0)

	switch requestClass {
		case "1":
			listHosts = append(listHosts, regions["EED"].classHosts["1"]...)
			listHosts = append(listHosts, regions["EED"].classHosts["2"]...)
			listHosts = append(listHosts, regions["EED"].classHosts["3"]...)
			listHosts = append(listHosts, regions["EED"].classHosts["4"]...)
			break
		case "2":
			listHosts = append(listHosts, regions["EED"].classHosts["2"]...)
			listHosts = append(listHosts, regions["EED"].classHosts["3"]...)
			listHosts = append(listHosts, regions["EED"].classHosts["4"]...)
			listHosts = append(listHosts, regions["EED"].classHosts["1"]...)
			break
		case "3":
			listHosts = append(listHosts, regions["EED"].classHosts["3"]...)
			listHosts = append(listHosts, regions["EED"].classHosts["4"]...)
			listHosts = append(listHosts, regions["EED"].classHosts["2"]...)
			listHosts = append(listHosts, regions["EED"].classHosts["1"]...)
			break
		case "4":
			listHosts = append(listHosts, regions["EED"].classHosts["4"]...)
			listHosts = append(listHosts, regions["EED"].classHosts["3"]...)
			listHosts = append(listHosts, regions["EED"].classHosts["2"]...)
			listHosts = append(listHosts, regions["EED"].classHosts["1"]...)
			break
	}
	return listHosts
}



//loosely sorted so that there's no big overhead in sorting
//func sort

/*
func CreatePersonEndpoint(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	var person Person
	_ = json.NewDecoder(req.Body).Decode(&person)
	person.ID = params["id"]
	people = append(people, person)
	json.NewEncoder(w).Encode(people)
}

func DeletePersonEndpoint(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	for index, item := range people {
		if item.ID == params["id"] {
			people = append(people[:index], people[index+1:]...)
			break
		}
	}
}
*/
func main() {
	regions = make(map[string]Region)	

	ServeSchedulerRequests()
}


func ServeSchedulerRequests() {
	router := mux.NewRouter()
	hosts = append(hosts, Host{HostID: "0", HostClass: "1", Region:"LEE", AvailableMemory: 5000000, AvailableCPUs: 50000000})
	hosts = append(hosts, Host{HostID: "2", HostClass: "2", Region:"LEE", AvailableMemory: 50000000000, AvailableCPUs: 50000000000})
	hosts = append(hosts, Host{HostID: "3", HostClass: "3", Region:"LEE", AvailableMemory: 50000000000, AvailableCPUs: 50000000000})
	hosts = append(hosts, Host{HostID: "4", HostClass: "4", Region:"LEE", AvailableMemory: 50000000000, AvailableCPUs: 50000000000})
	hosts = append(hosts, Host{HostID: "5", HostClass: "2", Region:"DEE", AvailableMemory: 50000000000, AvailableCPUs: 50000000000})
	hosts = append(hosts, Host{HostID: "7", HostClass: "1", Region:"EED", AvailableMemory: 50000000000, AvailableCPUs: 50000000000})
	hosts = append(hosts, Host{HostID: "8", HostClass: "1", Region:"DEE", AvailableMemory: 50000000000, AvailableCPUs: 50000000000})
	hosts = append(hosts, Host{HostID: "1", HostClass: "2", Region:"LEE", AvailableMemory: 5000000, AvailableCPUs: 50000000})

	classLEE := make(map[string][]*Host)
	classDEE := make(map[string][]*Host)
	classEED := make(map[string][]*Host)

	list1LEE := make([]*Host,0)
	list1DEE := make([]*Host,0)
	list1EED := make([]*Host,0)
	list2LEE := make([]*Host,0)
	list2DEE := make([]*Host,0)
	list3 := make([]*Host,0)
	list4 := make([]*Host,0)

	list1LEE = append(list1LEE, &hosts[0])
	list2LEE = append(list2LEE, &hosts[1])
	list3 = append(list3, &hosts[2])
	list4 = append(list4, &hosts[3])
	list2DEE = append(list2DEE, &hosts[4])
	list1EED = append(list1EED, &hosts[5])
	list1DEE = append(list1DEE, &hosts[6])
	list2LEE = append(list2LEE, &hosts[7])

	
	classLEE["1"] = list1LEE
	classLEE["2"] = list2LEE
	classLEE["3"] = list3
	classLEE["4"] = list4
	classDEE["1"] = list1DEE
	classDEE["2"] = list2DEE
	classEED["1"] = list1EED

	regions["LEE"] = Region{classLEE}
	regions["DEE"] = Region{classDEE}
	regions["EED"] = Region{classEED}


//	router.HandleFunc("/host/{hostid}", GetHost).Methods("GET")
	router.HandleFunc("/host/list/{requestclass}&{listtype}",GetListHostsLEE_DEE).Methods("GET")
	router.HandleFunc("/host/listkill/{requestclass}", GetListHostsEED_DEE).Methods("GET")
	router.HandleFunc("/host/updateclass/{requestclass}&{hostid}", UpdateHostClass).Methods("GET")
	router.HandleFunc("/host/createhost",CreateHost).Methods("POST")
	router.HandleFunc("/host/addworker/{hostid}&{workerid}",AddWorker).Methods("GET")
	router.HandleFunc("/host/updatetask/{taskid}&{newcpu}&{newmemory}",UpdateTaskResources).Methods("GET")
	router.HandleFunc("/host/killtask/{taskid}", KillTasks).Methods("GET")
	router.HandleFunc("/host/reschedule/{cpu}&{memory}&{requestclass}&{image}", RescheduleTask).Methods("GET")

//	router.HandleFunc("/people/{id}", GetPersonEndpoint).Methods("GET")
//	router.HandleFunc("/people/{id}", CreatePersonEndpoint).Methods("POST")
//	router.HandleFunc("/people/{id}", DeletePersonEndpoint).Methods("DELETE")
	log.Fatal(http.ListenAndServe("192.168.1.154:12345", router))

}


