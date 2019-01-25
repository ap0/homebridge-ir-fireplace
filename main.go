package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/sausheong/hs1xxplug"
	yaml "gopkg.in/yaml.v2"
)

type options struct {
	Port                 int
	RepeatCount          int
	Remote               remoteControl
	RemoteName           string
	IRSendPath           string
	OutletHost           string  `yaml:"outlet_host"`
	PowerOffThreshold    float64 `yaml:"power_off_threshold"`
	LowHeatLowThreshold  float64 `yaml:"medium_heat_low_threshold"`
	LowHeatHighThreshold float64 `yaml:"medium_heat_high_threshold"`
}

type PowerState string

const (
	Off       PowerState = "off"
	FlameOnly            = "flame_only"
	Low                  = "low"
	High                 = "high"
)

type remoteControl map[string]string

type fireplaceServer struct {
	opts   options
	mu     sync.Mutex
	remote remoteControl
	plug   hs1xxplug.Hs1xxPlug
}

// Just a subset of what this returns; all we need
type powerStats struct {
	Emeter struct {
		GetRealtime struct {
			Current float64 `json:"current"`
			Voltage float64 `json:"voltage"`
			Power   float64 `json:"power"`
		} `json:"get_realtime"`
	} `json:"emeter"`
}

func (f *fireplaceServer) getPowerStatus() (bool, error) {
	results, err := f.plug.MeterInfo()
	if err != nil {
		return false, fmt.Errorf("could not get plug meter info: %s", err.Error())
	}

	var stats powerStats
	if err := json.Unmarshal([]byte(results), &stats); err != nil {
		return false, fmt.Errorf("could not unmarshal power meter JSON: %s", err.Error())
	}

	return stats.Emeter.GetRealtime.Power > f.opts.PowerOffThreshold, nil
}

func (f *fireplaceServer) getHeatStatus() (PowerState, error) {

	results, err := f.plug.MeterInfo()
	if err != nil {
		return "", fmt.Errorf("could not get plug meter info: %s", err.Error())
	}

	var stats powerStats
	if err := json.Unmarshal([]byte(results), &stats); err != nil {
		return "", fmt.Errorf("could not unmarshal power meter JSON: %s", err.Error())
	}

	power := stats.Emeter.GetRealtime.Power

	switch {
	case power < f.opts.PowerOffThreshold:
		return Off, nil
	case power < f.opts.LowHeatLowThreshold:
		return FlameOnly, nil
	case power < f.opts.LowHeatHighThreshold:
		return Low, nil
	default:
		return High, nil
	}
}

func (f *fireplaceServer) getUsage(w http.ResponseWriter, r *http.Request) {
	results, err := f.plug.MeterInfo()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "error getting outlet status: %s", err.Error())
		return
	}

	var stats powerStats
	if err := json.Unmarshal([]byte(results), &stats); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "could not unmarshal power meter JSON: %s", err.Error())
		return
	}

	w.Header().Set("content-type", "application/json")
	json.NewEncoder(w).Encode(stats.Emeter.GetRealtime)
}

func (f *fireplaceServer) getStatus(w http.ResponseWriter, r *http.Request) {
	status, err := f.getPowerStatus()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "error getting outlet status: %s", err.Error())
		return
	}

	if status {
		fmt.Fprint(w, "1")
	} else {
		fmt.Fprint(w, "0")
	}
}

type heatInput struct {
	Value float64 `json:"value"`
}

func (f *fireplaceServer) setPower(w http.ResponseWriter, r *http.Request) {
	var h heatInput

	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&h); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "couldn't parse json: %s", err.Error())
		return
	}
	r.Body.Close()

	if h.Value == 0 {
		f.turnOff(w, r)
	} else {
		f.turnOn(w, r)
	}
}

func (f *fireplaceServer) setHeat(w http.ResponseWriter, r *http.Request, desiredState PowerState) {

	if desiredState == Off {
		f.turnOff(w, r)
		return
	}

	f.turnOn(w, r)

	if err := f.setToLevel(desiredState); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "error setting new state: %s", err.Error())
		return
	}
}

func (f *fireplaceServer) setToLevel(newLevel PowerState) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	curLevel, err := f.getHeatStatus()
	if err != nil {
		return err
	}

	var transitions []PowerState
	switch curLevel {
	case FlameOnly:
		switch newLevel {
		case Low:
			transitions = []PowerState{Low}
		case High:
			transitions = []PowerState{Low, High}
		}
	case Low:
		switch newLevel {
		case High:
			transitions = []PowerState{High}
		case FlameOnly:
			transitions = []PowerState{High, FlameOnly}
		}
	case High:
		switch newLevel {
		case FlameOnly:
			transitions = []PowerState{FlameOnly}
		case Low:
			transitions = []PowerState{FlameOnly, Low}
		}
	}

	for _, desiredState := range transitions {
		if err := f.waitForHeatState(desiredState); err != nil {
			return err
		}
	}

	return nil
}

func (f *fireplaceServer) heatLowOn(w http.ResponseWriter, r *http.Request) {
	f.setHeat(w, r, Low)
}

func (f *fireplaceServer) heatLowOff(w http.ResponseWriter, r *http.Request) {
	f.setHeat(w, r, FlameOnly)
}

func (f *fireplaceServer) heatHighOn(w http.ResponseWriter, r *http.Request) {
	f.setHeat(w, r, High)
}

func (f *fireplaceServer) heatHighOff(w http.ResponseWriter, r *http.Request) {
	f.setHeat(w, r, FlameOnly)
}

func (f *fireplaceServer) heatLowStatus(w http.ResponseWriter, r *http.Request) {
	f.heatStatus(w, Low)
}
func (f *fireplaceServer) heatHighStatus(w http.ResponseWriter, r *http.Request) {
	f.heatStatus(w, High)
}

func (f *fireplaceServer) heatStatus(w http.ResponseWriter, desiredStatus PowerState) {
	status, err := f.getHeatStatus()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "error getting outlet status: %s", err.Error())
		return
	}

	if status == desiredStatus {
		fmt.Fprintf(w, "1")
	} else {
		fmt.Fprintf(w, "0")
	}

}

func (f *fireplaceServer) waitForHeatState(desiredState PowerState) error {

	keyCode := f.opts.Remote["heat"]
	t := time.NewTimer(time.Second * 60)

	for {
		select {
		case <-t.C:
			return fmt.Errorf("timed out waiting for power state transition")
		default:
		}

		if err := func() error {
			// Allow sending only one command at a time
			if err := exec.Command(f.opts.IRSendPath,
				fmt.Sprintf("--count=%d", f.opts.RepeatCount),
				"SEND_ONCE",
				f.opts.RemoteName,
				keyCode).Run(); err != nil {
				return err
			}

			return nil
		}(); err != nil {
			return fmt.Errorf("error sending power command: %s", err.Error())
		}

		waitTimeout := time.NewTimer(time.Second * 20)

	timeoutLoop:
		for {
			select {
			case <-time.After(time.Millisecond * 250):
				status, err := f.getHeatStatus()
				if err != nil {
					return fmt.Errorf("error checking power status: %s", err.Error())
				}

				if status == desiredState {
					return nil
				}
			case <-waitTimeout.C:
				break timeoutLoop
			}
		}
	}
}

func (f *fireplaceServer) waitForPowerState(w http.ResponseWriter, on bool) {

	status, err := f.getPowerStatus()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "error checking power status: %s", err.Error())
		return
	}

	if status == on {
		return
	}

	t := time.NewTimer(time.Second * 30)

	keyCode := f.opts.Remote["power"]
	for {
		select {
		case <-t.C:
			w.WriteHeader(http.StatusRequestTimeout)
			fmt.Fprint(w, "timed out waiting for power state transition")
			return
		default:
		}

		if err := func() error {
			// Allow sending only one command at a time
			f.mu.Lock()
			defer f.mu.Unlock()
			if err := exec.Command(f.opts.IRSendPath,
				fmt.Sprintf("--count=%d", f.opts.RepeatCount),
				"SEND_ONCE",
				f.opts.RemoteName,
				keyCode).Run(); err != nil {
				return err
			}

			return nil
		}(); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "error sending power command: %s", err.Error())
			return
		}

		waitTimeout := time.NewTimer(time.Second * 15)

	timeoutLoop:
		for {
			select {
			case <-time.After(time.Millisecond * 250):
				status, err := f.getPowerStatus()
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					fmt.Fprintf(w, "error checking power status: %s", err.Error())
					return
				}

				if status == on {
					return
				}
			case <-waitTimeout.C:
				break timeoutLoop
			}
		}

	}
}

func (f *fireplaceServer) turnOn(w http.ResponseWriter, r *http.Request) {
	f.waitForPowerState(w, true)
}

func (f *fireplaceServer) turnOff(w http.ResponseWriter, r *http.Request) {
	f.waitForPowerState(w, false)
}

func (f *fireplaceServer) sendCommand(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	command := vars["command"]
	if command == "power" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "use the /power/on and /power/off routes for power")
		return
	}

	keyCode := f.remote[command]
	if keyCode == "" {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "could not find key code for command %s", command)
		return
	}

	if err := func() error {
		// Allow sending only one command at a time
		f.mu.Lock()
		defer f.mu.Unlock()
		if err := exec.Command(f.opts.IRSendPath,
			fmt.Sprintf("--count=%d", f.opts.RepeatCount),
			"SEND_ONCE",
			f.opts.RemoteName,
			keyCode).Run(); err != nil {
			return err
		}

		return nil
	}(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "error executing command: %s", err.Error())
		return
	}
}

func main() {

	opts := options{
		Port:                 8080,
		RepeatCount:          5,
		IRSendPath:           "/usr/bin/irsend",
		RemoteName:           "fireplace",
		PowerOffThreshold:    1.0,
		LowHeatLowThreshold:  650.0,
		LowHeatHighThreshold: 750.0,
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

	if _, ok := opts.Remote["power"]; !ok {
		log.Fatal("must specify a remote with a power button")
	}

	if opts.OutletHost == "" {
		log.Fatal("must specify outlet_host in config!")
	}

	rtr := mux.NewRouter()
	f := &fireplaceServer{
		opts:   opts,
		remote: opts.Remote,
		plug:   hs1xxplug.Hs1xxPlug{IPAddress: opts.OutletHost},
	}

	rtr.HandleFunc("/send/{command}", f.sendCommand).Methods(http.MethodGet)

	rtr.HandleFunc("/power", f.setPower).Methods(http.MethodPost)
	rtr.HandleFunc("/power/on", f.turnOn).Methods(http.MethodGet)
	rtr.HandleFunc("/power/off", f.turnOff).Methods(http.MethodGet)
	rtr.HandleFunc("/power/status", f.getStatus).Methods(http.MethodGet)

	rtr.HandleFunc("/heat/low/on", f.heatLowOn).Methods(http.MethodGet)
	rtr.HandleFunc("/heat/low/off", f.heatLowOff).Methods(http.MethodGet)
	rtr.HandleFunc("/heat/low/status", f.heatLowStatus).Methods(http.MethodGet)

	rtr.HandleFunc("/heat/high/on", f.heatHighOn).Methods(http.MethodGet)
	rtr.HandleFunc("/heat/high/off", f.heatHighOff).Methods(http.MethodGet)
	rtr.HandleFunc("/heat/high/status", f.heatHighStatus).Methods(http.MethodGet)

	rtr.HandleFunc("/energy/usage", f.getUsage).Methods(http.MethodGet)

	srv := &http.Server{
		Handler: rtr,
		Addr:    fmt.Sprintf(":%d", opts.Port),
	}

	if _, err := f.getPowerStatus(); err != nil {
		log.Fatalf("could not get power status: %s", err.Error())
	}

	log.Printf("Listening on :%d...\n", opts.Port)
	log.Fatal(srv.ListenAndServe())
}
