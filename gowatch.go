package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Config store values from JSON config file
type Config struct {
	Timeout int
	Watch   map[string]interface{}
}

// Run store running values
type Run struct {
	watcher        *fsnotify.Watcher
	ActiveWatchs   []string
	ListeningQueue string
	ExecutingQueue string
	InExecution    bool
	Queues         map[string]map[string]map[string]string
	WatchingDirs   map[string][]string
	LastEventTime  int64
}

// ParValues stores command line parameters
type ParValues struct {
	ConfigFile string
	Watch      string
	Help       bool
}

var config Config
var run Run
var parValues ParValues

func inArray(array []string, value string) bool {

	for _, a := range array {
		if a == value {
			return true
		}
	}
	return false
}

func addToQueue(event fsnotify.Event) {
	now := time.Now().UnixNano()
	run.LastEventTime = now

	watchKeys := reflect.ValueOf(config.Watch).MapKeys()

	for i := range watchKeys {
		watchK := watchKeys[i].String()

		if !isActiveWatch(watchK) {
			continue
		}

		if run.Queues[run.ListeningQueue][watchK] == nil {
			run.Queues[run.ListeningQueue][watchK] = map[string]string{}
		}
		SourceDir := getJSONValue("SourceDir", config.Watch[watchK]).(string)
		if strings.HasPrefix(event.Name, SourceDir) {
			if fileOp := run.Queues[run.ListeningQueue][watchK][event.Name]; fileOp == "" {
				run.Queues[run.ListeningQueue][watchK][event.Name] = event.Op.String()
			}
			return
		}
	}
	log.Panicf("Path not found (%v)", watchKeys)
}

func removeFromWatchingDir(watchK string, path string) {
	var newWatchingDirs []string

	for i := range run.WatchingDirs[watchK] {
		if run.WatchingDirs[watchK][i] == path ||
			(len(run.WatchingDirs[watchK][i]) > len(path) &&
				(run.WatchingDirs[watchK][i][:len(path)+1] == path+string(os.PathSeparator))) {

			run.watcher.Remove(path)
		} else {
			newWatchingDirs = append(newWatchingDirs, run.WatchingDirs[watchK][i])
		}
	}

	run.WatchingDirs[watchK] = newWatchingDirs
}

func addToWatchingDir(watchK string, path string, f os.FileInfo) error {

	if f == nil {
		err := fmt.Sprintf("Error: Cannot access to path %v", path)
		return errors.New(err)
	}
	if run.WatchingDirs == nil {
		run.WatchingDirs = map[string][]string{}
	}

	if run.WatchingDirs[watchK] == nil {
		run.WatchingDirs[watchK] = []string{}
	}

	exclude := getJSONValue("Exclude", config.Watch[watchK])
	if exclude != nil {
		excluding := exclude.([]interface{})
		for i := range excluding {

			foundPath := path
			if string(foundPath[len(foundPath)-1:]) != string(os.PathSeparator) {
				foundPath = foundPath + string(os.PathSeparator)
			}
			configExcluding := excluding[i].(string)
			if string(configExcluding[len(configExcluding)-1:]) != string(os.PathSeparator) {
				configExcluding = configExcluding + string(os.PathSeparator)
			}

			if len(foundPath) >= len(configExcluding) {
				if string(foundPath[0:len(configExcluding)]) == configExcluding {
					return nil
				}
			}
		}
	}

	if !inArray(run.WatchingDirs[watchK], path) && f.IsDir() {
		run.WatchingDirs[watchK] = append(run.WatchingDirs[watchK], path)

		if err := run.watcher.Add(path); err != nil {
			log.Fatal(err)
		}
	}

	return nil
}

func hasTimedOut() bool {
	timeDiff := time.Now().UnixNano() - run.LastEventTime
	return time.Duration(timeDiff) > (time.Duration(config.Timeout) * time.Millisecond)
}

func isDataRunQueues() bool {
	queueWatchKeys := reflect.ValueOf(run.Queues[run.ListeningQueue]).MapKeys()
	for watchK := range queueWatchKeys {
		if len(run.Queues[run.ListeningQueue][queueWatchKeys[watchK].String()]) > 0 {
			return true
		}
	}
	return false
}

func parseCmdParameters(watchK string, parameters string, eventName string) []string {
	// 2 loops for making sure the parameters placeholders are also replaced
	for range []int{1, 2} {
		placeHolders := regexp.MustCompile("{{(.+?)}}").FindAllStringSubmatch(parameters, -1)
		if len(placeHolders) > 0 {
			var value string
			for pIndex := range placeHolders {
				if placeHolders[pIndex][0] == "{{EventName}}" {
					value = eventName
				} else {
					value = getJSONValue(placeHolders[pIndex][1], config.Watch[watchK]).(string)
				}
				parameters = regexp.MustCompile(placeHolders[pIndex][0]).ReplaceAllString(parameters, value)
			}
		}
	}

	return []string{parameters}
}

func getCmdAndParameters(watchK string, cmdK int, eventName string) (bool, string, []string) {
	var isACommandWithEventName bool

	var parameters []string

	isACommandWithEventName = false
	Commands := getJSONValue("Cmd", config.Watch[watchK]).([]interface{})

	for i := range Commands[cmdK].([]interface{}) {
		p := Commands[cmdK].([]interface{})[i].(string)
		if len(p) > 0 {
			isACommandWithEventName = isACommandWithEventName || strings.Contains(p, "{{EventName}}")
		}
		parameters = append(parameters, parseCmdParameters(watchK, p, eventName)...)
	}

	if len(parameters) > 1 {
		return isACommandWithEventName, parameters[0], parameters[1:]
	}

	return false, parameters[0], []string{}
}

func switchQueues() {
	if run.ListeningQueue == "A" {
		run.ListeningQueue = "B"
		run.ExecutingQueue = "A"
	} else {
		run.ListeningQueue = "A"
		run.ExecutingQueue = "B"
	}
}

func updateWatchingDirs(watchK string) {
	var eventPath string
	paths := reflect.ValueOf(run.Queues[run.ExecutingQueue][watchK]).MapKeys()

	for i := range paths {
		// 1 CREATE - 2 WRITE - 4 REMOVE - 8 RENAME - 10 CHMOD
		eventPath = paths[i].String()
		eventType := string(run.Queues[run.ExecutingQueue][watchK][eventPath])

		if eventType == "REMOVE" || eventType == "RENAME" {
			removeFromWatchingDir(watchK, eventPath)
		}

		if f, err := os.Stat(eventPath); err == nil {
			if f.IsDir() {
				if eventType == "CREATE" {

					if err := filepath.Walk(
						eventPath,
						func(path string, f os.FileInfo, err error) error {
							return addToWatchingDir(watchK, path, f)
						}); err != nil {
						fmt.Printf("Error: addToWatchingDir\n %v\n", err.Error())
					}
				}
			}
		}
	}
}

func executeCommand(command string, args []string) {
	cmd := exec.Command(command, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()
}

func executeActions() {
	var command string
	var parameters []string
	var isACommandWithEventName bool
	var executeOnlyOnceForTheWatch bool

	switchQueues()

	run.InExecution = true

	queueWatchKeys := reflect.ValueOf(run.Queues[run.ExecutingQueue]).MapKeys()

	for i := range queueWatchKeys {
		watchK := queueWatchKeys[i].String()
		Commands := getJSONValue("Cmd", config.Watch[watchK]).([]interface{})
		eventNames := reflect.ValueOf(run.Queues[run.ExecutingQueue][watchK]).MapKeys()

		for cmdK := range Commands {
			isACommandWithEventName = false
			executeOnlyOnceForTheWatch = true
			for j := range eventNames {
				isACommandWithEventName, command, parameters = getCmdAndParameters(watchK, cmdK, eventNames[j].String())

				if isACommandWithEventName {
					executeOnlyOnceForTheWatch = false
				}

				executeCommand(command, parameters)

				if executeOnlyOnceForTheWatch {
					break
				}
			}
		}

		updateWatchingDirs(watchK)

		run.Queues[run.ExecutingQueue][watchK] = map[string]string{}
	}

	run.InExecution = false
}

func isActiveWatch(watchK string) bool {
	return reflect.DeepEqual(run.ActiveWatchs, []string{"*"}) || inArray(run.ActiveWatchs, watchK)
}

func watchDirectories() {

	configWatchKeys := reflect.ValueOf(config.Watch).MapKeys()

	for watchKeysIndex := range configWatchKeys {
		watchK := configWatchKeys[watchKeysIndex].String()

		if !isActiveWatch(watchK) {
			continue
		}
		SourceDir := getJSONValue("SourceDir", config.Watch[watchK]).(string)

		fmt.Printf("Watching project: %v\n", watchK)
		if err := filepath.Walk(
			SourceDir,
			func(path string, f os.FileInfo, err error) error {
				return addToWatchingDir(watchK, path, f)
			}); err != nil {
			showErrorAndExit(err.Error())
		}
	}

}

func getJSONValue(index string, source interface{}) interface{} {

	for k, v := range source.(map[string]interface{}) {
		if k == index {
			return v
		}
	}

	return nil
}

func showErrorAndExit(errorMessage string, args ...interface{}) {
	fmt.Printf(errorMessage, args...)
	os.Exit(1)
}

func validateConfig() {
	watchKeys := reflect.ValueOf(config.Watch).MapKeys()

	for i := range watchKeys {

		watchDataK := reflect.ValueOf(config.Watch[watchKeys[i].String()]).MapKeys()
		for j := range watchDataK {

			switch watchDataK[j].String() {
			case "SourceDir":
				if reflect.TypeOf(config.Watch[watchKeys[i].String()].(map[string]interface{})["SourceDir"]).String() != "string" {
					showErrorAndExit("Error: Unexpected config format for Watch(%v:%v) - Expected format: string\n", watchKeys[i], watchDataK[j])
				}
				break
			case "Cmd":
				if reflect.TypeOf(config.Watch[watchKeys[i].String()].(map[string]interface{})["Cmd"]).String() != "[]interface {}" {
					showErrorAndExit("Error: Unexpected config format for Watch(%v:%v) - Expected format: []string\n", watchKeys[i], watchDataK[j])
				}
				for k := range config.Watch[watchKeys[i].String()].(map[string]interface{})["Cmd"].([]interface{}) {
					cmd := config.Watch[watchKeys[i].String()].(map[string]interface{})["Cmd"].([]interface{})
					if len(cmd[k].([]interface{})) == 0 {
						showErrorAndExit("Error: No action cmd defined for Watch(%v:%v)\n", watchKeys[i].String(), k+1)
					}

					if len(cmd[k].([]interface{})[0].(string)) == 0 {
						showErrorAndExit("Error: Action cmd invalid for Watch(%v:%v)\n", watchKeys[i].String(), k+1)
					}
				}
				break
			}
		}
	}
}

func readConfig(ConfigFile string) {
	f, err := os.Open(ConfigFile)
	if err != nil {
		showErrorAndExit("Error: cannot open config file (%s)\n", ConfigFile)
	}
	defer f.Close()

	if json.NewDecoder(f).Decode(&config) != nil {
		showErrorAndExit("Error: cannot decode JSON file %s\n", ConfigFile)
	}
}

func parseParams() {
	flag.StringVar(&parValues.ConfigFile, "config", "gowatch.json", "Configuration filename")
	flag.StringVar(&parValues.Watch, "watch", "*", "List of projects to watch i.e: <watch_project1>,<watch_project2>")
	flag.BoolVar(&parValues.Help, "help", false, "Displays this help")
	flag.Parse()
	if parValues.Help {
		flag.Usage()
		os.Exit(0)
	}
}

func initizalize() {

	run.Queues = map[string]map[string]map[string]string{
		"A": {},
		"B": {},
	}
	run.ListeningQueue = "A"
	run.InExecution = false
	if len(parValues.Watch) > 0 {
		run.ActiveWatchs = strings.Split(parValues.Watch, ",")
	}

	var err error
	run.watcher, err = fsnotify.NewWatcher()

	if err != nil {
		log.Fatal(err)
	}
}

func listenToEvents() {
	done := make(chan bool)

	go func() {
		for {
			select {
			case event := <-run.watcher.Events:
				addToQueue(event)
			case err := <-run.watcher.Errors:
				log.Println("WATCHER ERROR:", err.Error())
			default:
				if !run.InExecution && hasTimedOut() && isDataRunQueues() {
					executeActions()
				}
				time.Sleep(time.Duration(150) * time.Millisecond)
			}
		}
	}()

	defer run.watcher.Close()

	<-done
}

func main() {
	parseParams()
	readConfig(parValues.ConfigFile)
	validateConfig()
	initizalize()
	watchDirectories()
	listenToEvents()
}
