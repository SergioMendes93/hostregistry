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
        HostID      string              `json:"hostid,omitempty"`
        WorkerNodesID []string          `json:"workernodesid,omitempty"`
        WorkerNodes []*node.Node		`json:"workernodes,omitempty"`
		HostClass   string              `json:"hostclass,omitempty"`
        Region      string              `json:"region,omitempty"`
        TotalResourcesUtilization int   `json:"totalresouces,omitempty"`
        CPU_Utilization int             `json:"cpu,omitempty"`
        MemoryUtilization int           `json:"memory,omitempty"`
		AvailableMemory int64			`json:"availablememory,omitempty"`
		AvailableCPUs int64				`json:"availablecpus,omitempty"`
        AllocatedResources int          `json:"resoucesallocated,omitempty"`
        TotalHostResources int          `json:"totalresources,omitempty"`
        OverbookingFactor int           `json:"overbookingfactor,omitempty"`
}


//Each region will have 4 lists, one for each overbooking class
//LEE=Lowest Energy Efficiency
type RegionLEE struct {
	Class1Hosts []*Host 	`json:"class1host, omitempty"`
	Class2Hosts []*Host		`json:"class2host, omitempty"`	
	Class3Hosts []*Host 	`json:"class3host, omitempty"`
	Class4Hosts []*Host 	`json:"class4host, omitempty"`
}

//DEE=Desired Energy Efficiency
type RegionDEE struct {
	Class1Hosts []*Host 	`json:"class1host, omitempty"`
	Class2Hosts []*Host		`json:"class2host, omitempty"`	
	Class3Hosts []*Host 	`json:"class3host, omitempty"`
	Class4Hosts []*Host 	`json:"class4host, omitempty"`
}

//EED=Energy Efficiency Degradation
type RegionEED struct {
	Class1Hosts []*Host 	`json:"class1host, omitempty"`
	Class2Hosts []*Host		`json:"class2host, omitempty"`	
	Class3Hosts []*Host 	`json:"class3host, omitempty"`
	Class4Hosts []*Host 	`json:"class4host, omitempty"`
}

var regionLEEHosts RegionLEE
var regionDEEHosts RegionDEE
var regionEEDHosts RegionEED
var hosts []Host

var lockRegionLEE = &sync.Mutex{}
var lockRegionDEE = &sync.Mutex{}
var lockRegionEED = &sync.Mutex{}
var lockHosts = &sync.Mutex{}

func KillTasks(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	taskID := parans["taskid"]

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
	regionLEEHosts.Class1Hosts = append(regionLEEHosts.Class1Hosts, &hosts[len(hosts)-1])	
	fmt.Println(regionLEEHosts.Class1Hosts)	

	for i := 0; i < len(regionLEEHosts.Class1Hosts); i++ {
		if regionLEEHosts.Class1Hosts[i].HostID == "1" {
		fmt.Println(*regionLEEHosts.Class1Hosts[i])
		}
	}
	/*for _, hostB := *regionLEEHosts.Class1Hosts {
		fmt.Println(hostB)
	} */
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
func UpdateHostClass(w http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	newHostClass := params["requestclass"]
	hostID := params["hostid"]

	for index, host := range hosts {
		lockHosts.Lock()
		if host.HostID == hostID && host.HostClass < newHostClass { //we only update the host class if the current class is lower
			hosts[index].HostClass = newHostClass
		}
		lockHosts.Unlock()
		return
	}
}

func UpdateHostRegion(hostID string, newRegion string) {

	for index, host := range hosts {
		if host.HostID == hostID {
			lockHosts.Lock()
			hosts[index].Region = newRegion
			lockHosts.Unlock()
			return 
		}
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
		listHosts = append(listHosts, regionLEEHosts.Class1Hosts...)
	} else if requestClass == "2" {
		listHosts = append(listHosts, regionLEEHosts.Class1Hosts...)
		listHosts = append(listHosts, regionLEEHosts.Class2Hosts...)
	} else if requestClass == "3" {
		listHosts = append(listHosts, regionLEEHosts.Class1Hosts...)
		listHosts = append(listHosts, regionLEEHosts.Class2Hosts...)
		listHosts = append(listHosts, regionLEEHosts.Class3Hosts...)
	} else if requestClass == "4" {
		listHosts = append(listHosts, regionLEEHosts.Class1Hosts...)
		listHosts = append(listHosts, regionLEEHosts.Class2Hosts...)
		listHosts = append(listHosts, regionLEEHosts.Class3Hosts...)
		listHosts = append(listHosts, regionLEEHosts.Class4Hosts...)
	}
	
	return listHosts
}

//for CUT algorithm
func GetHostsLEE_cut(requestClass string) ([]*Host) {
	//we only get hosts that respect requestClass <= hostClass and order them by ascending order of their class 
	//class 1 hosts are always selected

	listHosts := make([]*Host,0)
	
	if requestClass == "1" {
		listHosts = append(listHosts, regionLEEHosts.Class1Hosts...)
		listHosts = append(listHosts, regionLEEHosts.Class2Hosts...)
		listHosts = append(listHosts, regionLEEHosts.Class3Hosts...)
		listHosts = append(listHosts, regionLEEHosts.Class4Hosts...)
	} else if requestClass == "2" {
		listHosts = append(listHosts, regionLEEHosts.Class2Hosts...)
		listHosts = append(listHosts, regionLEEHosts.Class3Hosts...)
		listHosts = append(listHosts, regionLEEHosts.Class4Hosts...)
	} else if requestClass == "3" {
		listHosts = append(listHosts, regionLEEHosts.Class3Hosts...)
		listHosts = append(listHosts, regionLEEHosts.Class4Hosts...)
	} else if requestClass == "4" {
		listHosts = append(listHosts, regionLEEHosts.Class4Hosts...)
	}
	
	return listHosts
}

//for initial scheduling algori
func GetHostsDEE_normal(requestClass string) ([]*Host) {
	//we only get hosts that respect requestClass <= hostClass and order them by ascending order of their class 
	//class 1 hosts are always selected
	listHosts := make([]*Host,0)
	
	if requestClass == "1" {
		listHosts = append(listHosts, regionDEEHosts.Class1Hosts...)
	} else if requestClass == "2" {
		listHosts = append(listHosts, regionDEEHosts.Class1Hosts...)
		listHosts = append(listHosts, regionDEEHosts.Class2Hosts...)
	} else if requestClass == "3" {
		listHosts = append(listHosts, regionDEEHosts.Class1Hosts...)
		listHosts = append(listHosts, regionDEEHosts.Class2Hosts...)
		listHosts = append(listHosts, regionDEEHosts.Class3Hosts...)
	} else if requestClass == "4" {
		listHosts = append(listHosts, regionDEEHosts.Class1Hosts...)
		listHosts = append(listHosts, regionDEEHosts.Class2Hosts...)
		listHosts = append(listHosts, regionDEEHosts.Class3Hosts...)
		listHosts = append(listHosts, regionDEEHosts.Class4Hosts...)
	}

	return listHosts
}

//for CUT algorithm
func GetHostsDEE_cut(requestClass string) ([]*Host) {
	//we only get hosts that respect requestClass <= hostClass and order them by ascending order of their class 
	//class 1 hosts are always selected
	listHosts := make([]*Host,0)
	
	if requestClass == "1" {
		listHosts = append(listHosts, regionDEEHosts.Class1Hosts...)
		listHosts = append(listHosts, regionDEEHosts.Class2Hosts...)
		listHosts = append(listHosts, regionDEEHosts.Class3Hosts...)
		listHosts = append(listHosts, regionDEEHosts.Class4Hosts...)
	} else if requestClass == "2" {
		listHosts = append(listHosts, regionDEEHosts.Class2Hosts...)
		listHosts = append(listHosts, regionDEEHosts.Class3Hosts...)
		listHosts = append(listHosts, regionDEEHosts.Class4Hosts...)
	} else if requestClass == "3" {
		listHosts = append(listHosts, regionDEEHosts.Class3Hosts...)
		listHosts = append(listHosts, regionDEEHosts.Class4Hosts...)
	} else if requestClass == "4" {
		listHosts = append(listHosts, regionDEEHosts.Class4Hosts...)
	}
	
	return listHosts
	
}

//for KILL algorithm
func GetHostsDEE_kill(requestClass string) ([]*Host) {
	listHosts := make([]*Host,0)

	switch requestClass {
		case "1":
			listHosts = append(listHosts, regionDEEHosts.Class1Hosts...)
			listHosts = append(listHosts, regionDEEHosts.Class2Hosts...)
			listHosts = append(listHosts, regionDEEHosts.Class3Hosts...)
			listHosts = append(listHosts, regionDEEHosts.Class4Hosts...)
			break
		case "2":
			listHosts = append(listHosts, regionDEEHosts.Class2Hosts...)
			listHosts = append(listHosts, regionDEEHosts.Class3Hosts...)
			listHosts = append(listHosts, regionDEEHosts.Class4Hosts...)
			listHosts = append(listHosts, regionDEEHosts.Class1Hosts...)
			break
		case "3":
			listHosts = append(listHosts, regionDEEHosts.Class3Hosts...)
			listHosts = append(listHosts, regionDEEHosts.Class4Hosts...)
			listHosts = append(listHosts, regionDEEHosts.Class2Hosts...)
			listHosts = append(listHosts, regionDEEHosts.Class1Hosts...)
			break
		case "4":
			listHosts = append(listHosts, regionDEEHosts.Class4Hosts...)
			listHosts = append(listHosts, regionDEEHosts.Class3Hosts...)
			listHosts = append(listHosts, regionDEEHosts.Class2Hosts...)
			listHosts = append(listHosts, regionDEEHosts.Class1Hosts...)
			break
	}
	return listHosts
}

func GetHostsEED(requestClass string) ([]*Host) {
	listHosts := make([]*Host,0)
	
	switch requestClass {
		case "1":
			listHosts = append(listHosts, regionEEDHosts.Class1Hosts...)
			listHosts = append(listHosts, regionEEDHosts.Class2Hosts...)
			listHosts = append(listHosts, regionEEDHosts.Class3Hosts...)
			listHosts = append(listHosts, regionEEDHosts.Class4Hosts...)
			break
		case "2":
			listHosts = append(listHosts, regionEEDHosts.Class2Hosts...)
			listHosts = append(listHosts, regionEEDHosts.Class3Hosts...)
			listHosts = append(listHosts, regionEEDHosts.Class4Hosts...)
			listHosts = append(listHosts, regionEEDHosts.Class1Hosts...)
			break
		case "3":
			listHosts = append(listHosts, regionEEDHosts.Class3Hosts...)
			listHosts = append(listHosts, regionEEDHosts.Class4Hosts...)
			listHosts = append(listHosts, regionEEDHosts.Class2Hosts...)
			listHosts = append(listHosts, regionEEDHosts.Class1Hosts...)
			break
		case "4":
			listHosts = append(listHosts, regionEEDHosts.Class4Hosts...)
			listHosts = append(listHosts, regionEEDHosts.Class3Hosts...)
			listHosts = append(listHosts, regionEEDHosts.Class2Hosts...)
			listHosts = append(listHosts, regionEEDHosts.Class1Hosts...)
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
	ServeSchedulerRequests()

}


func ServeSchedulerRequests() {
	router := mux.NewRouter()
	hosts = append(hosts, Host{HostID: "1", HostClass: "1", Region:"LEE", AvailableMemory: 5000000, AvailableCPUs: 50000000})
	hosts = append(hosts, Host{HostID: "2", HostClass: "2", Region:"LEE", AvailableMemory: 50000000000, AvailableCPUs: 50000000000})
	hosts = append(hosts, Host{HostID: "3", HostClass: "3", Region:"LEE", AvailableMemory: 50000000000, AvailableCPUs: 50000000000})
	hosts = append(hosts, Host{HostID: "4", HostClass: "4", Region:"LEE", AvailableMemory: 50000000000, AvailableCPUs: 50000000000})
	hosts = append(hosts, Host{HostID: "5", HostClass: "2", Region:"DEE", AvailableMemory: 50000000000, AvailableCPUs: 50000000000})
	hosts = append(hosts, Host{HostID: "7", HostClass: "1", Region:"EED", AvailableMemory: 50000000000, AvailableCPUs: 50000000000})
	hosts = append(hosts, Host{HostID: "8", HostClass: "1", Region:"DEE", AvailableMemory: 50000000000, AvailableCPUs: 50000000000})
		
	regionLEEHosts.Class1Hosts = append(regionLEEHosts.Class1Hosts, &hosts[0])	
	regionLEEHosts.Class2Hosts = append(regionLEEHosts.Class2Hosts,	&hosts[1])
	regionLEEHosts.Class3Hosts = append(regionLEEHosts.Class3Hosts, &hosts[2])
	regionLEEHosts.Class4Hosts = append(regionLEEHosts.Class4Hosts, &hosts[3])
	regionDEEHosts.Class2Hosts = append(regionDEEHosts.Class2Hosts, &hosts[4])
	regionEEDHosts.Class1Hosts = append(regionEEDHosts.Class1Hosts, &hosts[5])
	regionDEEHosts.Class1Hosts = append(regionDEEHosts.Class1Hosts, &hosts[6])

//	router.HandleFunc("/host/{hostid}", GetHost).Methods("GET")
	router.HandleFunc("/host/list/{requestclass}&{listtype}",GetListHostsLEE_DEE).Methods("GET")
	router.HandleFunc("/host/listkill/{requestclass}", GetListHostsEED_DEE).Methods("GET")
	router.HandleFunc("/host/updateclass/{requestclass}&{hostid}", UpdateHostClass).Methods("GET")
	router.HandleFunc("/host/createhost",CreateHost).Methods("POST")
	router.HandleFunc("/host/addworker/{hostid}&{workerid}",AddWorker).Methods("GET")
	router.HandleFunc("/host/updatetask/{taskid}&{newcpu}&{newmemory}",UpdateTaskResources).Methods("GET")
	router.HandleFunc("/host/killtask/{taskid}", KillTask).Methods("GET")

//	router.HandleFunc("/people/{id}", GetPersonEndpoint).Methods("GET")
//	router.HandleFunc("/people/{id}", CreatePersonEndpoint).Methods("POST")
//	router.HandleFunc("/people/{id}", DeletePersonEndpoint).Methods("DELETE")
	log.Fatal(http.ListenAndServe("192.168.1.154:12345", router))

}


