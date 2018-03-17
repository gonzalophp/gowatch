# gowatch

##### Filesystem watcher utility - (Golang)


```text
Usage of gowatch:
  -config string
	Configuration filename (default "gowatch.json")
  -help
    	Displays this help
  -watch string
    	List of projects to watch i.e: <watch_project1>,<watch_project2> (default "*")
```
<br/>

### Running gowatch

```

go run gowatch.go

gowatch -help
gowatch -config=/path/gowatch.json
gowatch -watch=my_project_1,my_project_3,my_project_4
gowatch -config=/path/gowatch.json -watch=my_project_4

```

### Config file

#### Required:
* **Timeout** [Int] Milliseconds before triggering actions
* **SourceDir** [String] Top level watching directory
* **Cmd** [List ] Execute system commands.
  - **Cmd Format**:
  ``` 
	 ["system_command1","parameter1","parameter2",...],
	 ["system_command2","parameter1","parameter2",...],
	 ["system_command3","parameter1","parameter2",...]
	...
  ```
  - **Cmd Parameters**:
    + **Placeholders**:
    ```
    {{Parameter_Name}} - Value defined in watch project
    
    All parameter_names are valid except the reserved placeholder "EventName" (described below)
    ```
     + **Reserved Placeholders**:
    ```
    {{EventName}} - Resource path receiving the filesystem event  
    
    *commands WITH {{EventName}} will be executed n times
    *commands WITHOUT {{EventName}} will be executed once
    ```
        
#### Optional:
* **Exclude** [List] Ignore changes under these directories 


### gowatch.json config file example:
```json
{
  "Timeout": 300,
  "Watch" : {
    "my_project_1": {
      "SourceDir": "/var/www/my_project_1",
      "Exclude":
      [
        "/var/www/my_project_1/vendor",
        "/var/www/my_project_1/.git"
      ],
      "Cmd": [
        ["echo","Resource modified: {{EventName}}"],
        ["ls","{{Par1}}","{{Par2}}","{{Par3}}","{{SourceDir}}"]
      ],
      "Par1": "-l",
      "Par2": "-a",
      "Par3": "-r"
    },
    "my_project_2": {
      "SourceDir": "/home/mydir/library1",
      "Cmd": [
        ["rsync","{{SourceDir}}","/var/www/library/library1"]
      ]
    },
    "my_project_3": {
      "SourceDir": "/home/mydir/library2",
      "Exclude":
      [
        "/home/mydir/library2/vendor"
      ],
      "Cmd": [
        ["rsync","{{SourceDir}}","{{Destination}}"]
      ],
      "Destination": "/var/www/library/library2"
    }
  }
}
```

## Install
#### Windows 10
* Download GoLang MSI installer from https://golang.org/dl/
* Building gowatch.exe:
```
git clone git@github.com:gonzalophp/gowatch.git
cd gowatch
go get "github.com/fsnotify/fsnotify"
go build gowatch.go
```

Windows users might need to setup their Cmd actions starting from **"cmd","/C"**

```
    "Cmd": [
        ["cmd","/C","echo","This filesystem resource has been modified:","{{EventName}}"]
    ],
```
