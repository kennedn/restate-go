# restate-go
A cloudless home automation solution powered by Golang.

Currently supported device types:

|Device|Description|
|---|---|
|alert| [Pushover](https://pushover.net/api#messages) message forwarding|
|meross|Control Meross bulb and sockets|
|snowdon| Control Snowdon II soundbars with the [Snowdon-II-wifi](https://github.com/kennedn/Snowdon-II-Wifi) mod|
|tvcom|Control LG5000 TVs over a [websocket serial bridge](https://github.com/kennedn/pico-ws-uart/)|
|wol|Control Wake-On-Lan enabled devices, power state can be toggled on devices utilising [Action-On-LAN](https://github.com/kennedn/Action-On-LAN)|

# Configuration

| Parameter     | Description                                      |
| ------------- | ------------------------------------------------ |
| `apiVersion`  | version string to be prepended to all endpoint routes |
| `devices`     | array of device objects |

## devices

### alert

| Parameter     | Description                                      |
| ------------- | ------------------------------------------------ |
| `name`        | Unique identifier for the alert device.         |
| `timeoutMs`   | Timeout value in milliseconds for the alert operation. |
| `token`       | Pushover application token. |
| `user`        | Pushover user token. |


### meross

| Parameter     | Description                                      |
| ------------- | ------------------------------------------------ |
| `name`        | Unique identifier for the device.               |
| `deviceType`  | Type of meross device: "bulb" or "socket".        |
| `timeoutMs`   | Timeout value in milliseconds for communication. |
| `host`        | IP address of the device.                      |

### snowdon

| Parameter     | Description                                      |
| ------------- | ------------------------------------------------ |
| `name`        | Unique identifier for the Snowdon device.       |
| `timeoutMs`   | Timeout value in milliseconds for communication. |
| `host`        | IP address of the Snowdon device.      |

### tvcom

| Parameter     | Description                                      |
| ------------- | ------------------------------------------------ |
| `name`        | Unique identifier for the tvcom device.         |
| `timeoutMs`   | Timeout value in milliseconds for communication. |
| `host`        | IP address of the tvcom websocket bridge         |


### wol

| Parameter     | Description                                      |
| ------------- | ------------------------------------------------ |
| `name`        | Unique identifier for the WOL device.           |
| `timeoutMs`   | Timeout value in milliseconds for the WOL operation. |
| `host`        | IP address of the target machine.               |
| `macAddress`  | MAC address of the target machine.              |

# Example

```yaml
apiVersion: v2
devices:
- type: meross
  config:
    name: "lamp"
    deviceType: bulb
    timeoutMs: 1600
    host: "10.0.0.140"
- type: meross
  config:
    name: "plug"
    deviceType: socket
    timeoutMs: 1600
    host: "10.0.0.150"
- type: tvcom
  config:
    name: tvcom
    timeoutMs: 1000
    host: "10.0.0.161"
- type: snowdon
  config:
    name: snowdon
    timeoutMs: 10000
    host: "10.0.0.160:8080"
- type: wol
  config:
    name: pc
    timeoutMs: 100
    host: "10.0.0.100"
    macAddress: "00:11:22:33:44:55"
- type: alert
  config:
    name: alert
    timeoutMs: 5000
    token: xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
    user: xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx

```