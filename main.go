package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"

	"github.com/gorilla/mux"
	yaml "gopkg.in/yaml.v2"
)

type options struct {
	Port        int
	RepeatCount int
	Remotes     map[string]map[string]string
	IRSendPath  string
}

type irButton struct {
	// we track a state to attempt to be useful to homekit
	state   bool
	keyCode string
}

type remoteControl map[string]*irButton

type fireplaceServer struct {
	opts    options
	mu      sync.Mutex
	remotes map[string]remoteControl
}

func (f *fireplaceServer) getStatus(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	remote, ok := f.remotes[vars["remote"]]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "could not find remote %s", vars["remote"])
		return
	}

	button := remote[vars["command"]]
	if button.keyCode == "" {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "could not find command %s on remote %s", vars["command"], vars["remote"])
		return
	}

	if button.state {
		fmt.Fprint(w, "1")
	} else {
		fmt.Fprint(w, "0")
	}
}

func (f *fireplaceServer) sendCommand(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	remote, ok := f.remotes[vars["remote"]]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "could not find remote %s", vars["remote"])
		return
	}

	button := remote[vars["command"]]
	if button.keyCode == "" {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "could not find command %s on remote %s", vars["command"], vars["remote"])
		return
	}

	newState := false
	if err := func() error {
		// Allow sending only one command at a time
		f.mu.Lock()
		defer f.mu.Unlock()
		if err := exec.Command(f.opts.IRSendPath,
			fmt.Sprintf("--count=%d", f.opts.RepeatCount),
			"SEND_ONCE",
			vars["remote"],
			button.keyCode).Run(); err != nil {
			return err
		}

		button.state = !button.state
		newState = button.state
		return nil
	}(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "error executing command: %s", err.Error())
		return
	}

	if newState {
		fmt.Fprintf(w, "1")
	} else {
		fmt.Fprint(w, "0")
	}

}

func main() {

	opts := options{
		Port:        8080,
		RepeatCount: 5,
		IRSendPath:  "/usr/bin/irsend",
	}

	if len(os.Args) < 2 {
		fmt.Printf("usage: %s config.yml\n", os.Args[0])
		os.Exit(1)
	}

	b, err := ioutil.ReadFile(os.Args[1])
	if err != nil {
		log.Fatalf("error reading config file: %s", err.Error())
	}

	if err := yaml.Unmarshal(b, &opts); err != nil {
		log.Fatalf("error parsing config file: %s", err.Error())
	}
	rtr := mux.NewRouter()
	f := &fireplaceServer{
		opts:    opts,
		remotes: make(map[string]remoteControl),
	}

	for remoteName, remote := range opts.Remotes {
		f.remotes[remoteName] = make(remoteControl)
		for buttonName, buttonKeyCode := range remote {
			f.remotes[remoteName][buttonName] = &irButton{
				state:   false,
				keyCode: buttonKeyCode,
			}
		}

	}

	rtr.HandleFunc("/send/{remote}/{command}", f.sendCommand).Methods(http.MethodGet)
	rtr.HandleFunc("/status/{remote}/{command}", f.getStatus).Methods(http.MethodGet)

	srv := &http.Server{
		Handler: rtr,
		Addr:    fmt.Sprintf(":%d", opts.Port),
	}

	log.Printf("Listening on :%d...\n", opts.Port)
	log.Fatal(srv.ListenAndServe())
}
