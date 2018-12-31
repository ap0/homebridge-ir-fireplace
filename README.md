# homebridge-ir-fireplace

Simple API server to receive HTTP commands and execute [LIRC](http://www.lirc.org/) `irsend` commands via an API, meant to be used in HomeKit. The API is compatible with [Homebrige](https://github.com/nfarina/homebridge) (specifically [homebridge-http-switch](https://github.com/Supereg/homebridge-http-switch)).  A big improvement would be to communicate with the `lircd` socket directly (and allow more options in the config).

The fireplace I wrote it to control has a single on/off button, so be careful using it in automations such as `Good Night` where you expect it to turn off, as it is possible to obstruct the infrared path.  I find it works best to configure it as a `toggle` switch in `homebridge-http-switch`.

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
repeat_count: 5
remotes:
        fireplace:
                power: key_power
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
            "onUrl": "http://firepi:8080/send/fireplace/power",
            "offUrl": "http://firepi:8080/send/fireplace/power",
            "statusUrl": "http://firepi:8080/status/fireplace/power"
        }
    ]
}
```
