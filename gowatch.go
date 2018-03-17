package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
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

// Cmd And Parameters
type CmdItem []string

// Watch settings
type Watch struct {
	SourceDir      string
	Exclude        []string
	Cmd            []CmdItem
	UserParameters map[string]string
}

func (w Watch) getParameter(p string) string {
	structValue := reflect.ValueOf(w).FieldByName(p)
	if structValue.IsValid() {
		return structValue.String()
	}

	if userValue, ok := w.UserParameters[p]; ok {
		return userValue
	}

	return ""
}

// Config store values from JSON config file
type Config struct {
	Timeout int64
	Watch   map[string]Watch
}

func (c Config) getTimeOut() int64 {
	return c.Timeout
}

func (c Config) getWatch(watchK string) Watch {
	return c.Watch[watchK]
}

// New : Config initizalizer
func (c Config) New(fileName string) Config {

	raw, err := ioutil.ReadFile(fileName)
	if err != nil {
		showErrorAndExit("Error: cannot open config file (%s)\n", fileName)
	}
	var configRaw map[string]interface{}
	json.Unmarshal(raw, &configRaw)
	json.Unmarshal(raw, &c)

	if configRaw["Watch"] == nil {
		showErrorAndExit("Error: cannot decode config file (%s)\n", fileName)
	}
	for watchK, watchData := range configRaw["Watch"].(map[string]interface{}) {
		if len(c.getWatch(watchK).SourceDir) == 0 {
			showErrorAndExit("Error: Watch(%v:%v) is empty\n", watchK, "SourceDir")
		}

		for k, v := range watchData.(map[string]interface{}) {
			if !reflect.ValueOf(c.Watch[watchK]).FieldByName(k).IsValid() {
				if c.Watch[watchK].UserParameters == nil {
					w := c.Watch[watchK]
					w.UserParameters = map[string]string{}
					c.Watch[watchK] = w
				}
				c.Watch[watchK].UserParameters[k] = v.(string)
			}
		}
	}

	return c
}

// WatchQueue
type WatchQueue map[string]string

// BufferQueue
type BufferQueue map[string]WatchQueue

// Run store running values
type Run struct {
	watcher        *fsnotify.Watcher
	ActiveWatchs   []string
	ListeningQueue string
	ExecutingQueue string
	InExecution    bool
	Queues         map[string]BufferQueue
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
	run.LastEventTime = time.Now().UnixNano()

	for _, watchK := range run.ActiveWatchs {
		if run.Queues[run.ListeningQueue][watchK] == nil {
			run.Queues[run.ListeningQueue][watchK] = map[string]string{}
		}
		SourceDir := config.getWatch(watchK).SourceDir
		if strings.HasPrefix(event.Name, SourceDir) {
			if fileOp := run.Queues[run.ListeningQueue][watchK][event.Name]; fileOp == "" {
				run.Queues[run.ListeningQueue][watchK][event.Name] = event.Op.String()
			}
			continue
		}
	}
}

func removeFromWatchingDir(watchK string, path string) {
	var newWatchingDirs []string

	for _, watchedDir := range run.WatchingDirs[watchK] {
		if watchedDir == path || (len(watchedDir) > len(path) &&
			(watchedDir[:len(path)+1] == path+string(os.PathSeparator))) {

			run.watcher.Remove(path)
		} else {
			newWatchingDirs = append(newWatchingDirs, watchedDir)
		}
	}

	run.WatchingDirs[watchK] = newWatchingDirs
}

func addToWatchingDir(watchK string, path string, f os.FileInfo) error {

	if f == nil {
		err := fmt.Sprintf("Error: Cannot access to path %v\n", path)
		return errors.New(err)
	}
	if run.WatchingDirs == nil {
		run.WatchingDirs = map[string][]string{}
	}

	if run.WatchingDirs[watchK] == nil {
		run.WatchingDirs[watchK] = []string{}
	}

	exclude := config.getWatch(watchK).Exclude
	if exclude != nil {
		for _, excludedDir := range exclude {

			foundPath := path
			if string(foundPath[len(foundPath)-1:]) != string(os.PathSeparator) {
				foundPath = foundPath + string(os.PathSeparator)
			}

			if string(excludedDir[len(excludedDir)-1:]) != string(os.PathSeparator) {
				excludedDir = excludedDir + string(os.PathSeparator)
			}

			if len(foundPath) >= len(excludedDir) {
				if string(foundPath[0:len(excludedDir)]) == excludedDir {
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
	for watchK := range run.Queues[run.ListeningQueue] {
		if len(run.Queues[run.ListeningQueue][watchK]) > 0 {
			return true
		}
	}
	return false
}

func parseCmdItemParameters(watchK string, parameters string, eventName string) []string {
	// 2 loops for making sure the parameters placeholders are also replaced
	for range []int{1, 2} {
		placeHolders := regexp.MustCompile("{{(.+?)}}").FindAllStringSubmatch(parameters, -1)
		if len(placeHolders) > 0 {
			var value string
			for pIndex := range placeHolders {
				if placeHolders[pIndex][0] == "{{EventName}}" {
					value = eventName
				} else {
					value = config.getWatch(watchK).getParameter(placeHolders[pIndex][1])
				}
				parameters = regexp.MustCompile(placeHolders[pIndex][0]).ReplaceAllString(parameters, value)
			}
		}
	}

	return []string{parameters}
}

func getCmdItemAndParameters(watchK string, CmdItemK int, eventName string) (bool, string, []string) {
	var isACommandWithEventName = false
	var params []string

	CmdList := config.getWatch(watchK).Cmd

	for _, param := range CmdList[CmdItemK] {
		if len(param) > 0 {
			isACommandWithEventName = isACommandWithEventName || strings.Contains(param, "{{EventName}}")
		}
		params = append(params, parseCmdItemParameters(watchK, param, eventName)...)
	}

	if len(params) > 1 {
		return isACommandWithEventName, params[0], params[1:]
	}

	return false, params[0], []string{}
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

	for eventPath := range run.Queues[run.ExecutingQueue][watchK] {
		// 1 CREATE - 2 WRITE - 4 REMOVE - 8 RENAME - 10 CHMOD
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
	CmdItem := exec.Command(command, args...)
	CmdItem.Stdout = os.Stdout
	CmdItem.Stderr = os.Stderr
	CmdItem.Run()
}

func executeActions() {
	var command string
	var parameters []string
	var isACommandWithEventName bool
	var executeOnlyOnceForTheWatch bool

	switchQueues()

	run.InExecution = true

	for watchK := range run.Queues[run.ExecutingQueue] {
		CmdList := config.getWatch(watchK).Cmd
		for CmdItemK := range CmdList {
			isACommandWithEventName = false
			executeOnlyOnceForTheWatch = true
			for eventName := range run.Queues[run.ExecutingQueue][watchK] {
				isACommandWithEventName, command, parameters = getCmdItemAndParameters(watchK, CmdItemK, eventName)

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

func addListenersToDirectories() {
	for _, watchK := range run.ActiveWatchs {
		SourceDir := config.getWatch(watchK).SourceDir
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

func showErrorAndExit(errorMessage string, args ...interface{}) {
	fmt.Printf(errorMessage, args...)
	os.Exit(1)
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

	run.Queues = map[string]BufferQueue{
		"A": {},
		"B": {},
	}
	run.ListeningQueue = "A"
	run.InExecution = false
	if parValues.Watch == "*" {
		for k := range config.Watch {
			run.ActiveWatchs = append(run.ActiveWatchs, k)
		}
	} else {
		run.ActiveWatchs = strings.Split(parValues.Watch, ",")
	}

	var err error

	run.watcher, err = fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
}

func waitForEvents() {
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
	config = config.New(parValues.ConfigFile)
	initizalize()
	addListenersToDirectories()
	waitForEvents()
}
