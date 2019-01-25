# homebridge-ir-fireplace

Simple API server to receive HTTP commands and execute [LIRC](http://www.lirc.org/) `irsend` commands via an API, meant to be used in HomeKit.  The power state of the fireplace is tracked by querying a [TP-LINK HS110 Smart Plug](https://www.tp-link.com/us/products/details/cat-5258_HS110.html), which can be queried for energy usage details on your home network without having to rely on a cloud service, which makes it both low latency and not dependent on Internet access.  By tracking actual power usage, you can ensure that the fireplace has actually been turned on or off.

This also supports a heater. Mine has three states: on (flame only), low, and high, and the IR command cycles through each state.  The app will manage state transitions and monitor power consumption to ensure each transition happens successfully before returning.

The API is compatible with [Homebrige](https://github.com/nfarina/homebridge) (specifically [homebridge-http-switch](https://github.com/Supereg/homebridge-http-switch)).  A big improvement would be to communicate with the `lircd` socket directly (and allow more options in the config).

I used a [Raspberry Pi IR shield](http://www.raspberrypiwiki.com/index.php/Raspberry_Pi_IR_Control_Expansion_Board) with `LIRC` for this.  You can pick them up on Amazon for about $11.

This uses go modules and thus requires go1.11.

## Building: 
```
go build
# build for raspberry pi
GOOS=linux GOARM=7 GOARCH=arm go build

./homebridge-ir-fireplace config.yml
```

## Configuration

Remotes map to different remotes in `lirc`.  Each remote in `lirc` has a key code it's associated with (usually namespaced as `KEY_*` or `BTN_*`).  The config will map a friendly route name to the key name.

Example `config.yml`:

```yml
repeat_count: 5 # number of times to repeat commands (helpful for raw mode captures)
min_power_threshold: 4.9 # energy usage to determine whether it's on or off
remote_name: fireplace # The name of the remote in LIRC
outlet_host: 10.0.0.20 # IP of the HS110 outlet
remote:
        power: key_power # power is required; all other fields are optional
        timer: key_time
        heat: key_mute
        flame_down: key_volumedown
        flame_up: key_volumeup
```

So in this example, `GET /send/fireplace/power` would execute `irsend --count=5 SEND_ONCE fireplace key_power`.

Example Homebridge config:

```json
{
    "accessories": [
        {
            "accessory": "HTTP-SWITCH",
            "name": "Fireplace Power",
            "switchType": "stateful",
            "onUrl": "http://firepi:8080/power/on",
            "offUrl": "http://firepi:8080/power/off",
            "statusUrl": "http://firepi:8080/power/status",
            "pullInterval": 5000
        },
        {
            "accessory": "HTTP-SWITCH",
            "name": "Fireplace Heat Low",
            "switchType": "stateful",
            "onUrl": "http://firepi:8080/heat/low/on",
            "offUrl": "http://firepi:8080/heat/low/off",
            "statusUrl": "http://firepi:8080/heat/low/status",
            "pullInterval": 5000
        },
        {
            "accessory": "HTTP-SWITCH",
            "name": "Fireplace Heat High",
            "switchType": "stateful",
            "onUrl": "http://firepi:8080/heat/high/on",
            "offUrl": "http://firepi:8080/heat/high/off",
            "statusUrl": "http://firepi:8080/heat/high/status",
            "pullInterval": 5000
        }
    ]
}
```
