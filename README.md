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
|frigate|Subscribes to the `frigate/reviews` topic and forwards any alerts to [Pushover](https://pushover.net/api#messages)|

## Configuration

| Parameter     | Description                                      |
| ------------- | ------------------------------------------------ |
| `apiVersion`  | version string to be prepended to all endpoint routes |
| `devices`     | array of device objects |

### devices

#### alert

| Parameter     | Description                                      |
| ------------- | ------------------------------------------------ |
| `name`        | Unique identifier for the alert device.         |
| `timeoutMs`   | Timeout value in milliseconds for the alert operation. |
| `token`       | Pushover application token. |
| `user`        | Pushover user token. |

#### meross

| Parameter     | Description                                      |
| ------------- | ------------------------------------------------ |
| `name`        | Unique identifier for the device.               |
| `deviceType`  | Type of meross device: "bulb" or "socket".        |
| `timeoutMs`   | Timeout value in milliseconds for communication. |
| `host`        | IP address of the device.                      |

#### snowdon

| Parameter     | Description                                      |
| ------------- | ------------------------------------------------ |
| `name`        | Unique identifier for the Snowdon device.       |
| `timeoutMs`   | Timeout value in milliseconds for communication. |
| `host`        | IP address of the Snowdon device.      |

#### tvcom

| Parameter     | Description                                      |
| ------------- | ------------------------------------------------ |
| `name`        | Unique identifier for the tvcom device.         |
| `timeoutMs`   | Timeout value in milliseconds for communication. |
| `host`        | IP address of the tvcom websocket bridge         |

#### wol

| Parameter     | Description                                      |
| ------------- | ------------------------------------------------ |
| `name`        | Unique identifier for the WOL device.           |
| `timeoutMs`   | Timeout value in milliseconds for the WOL operation. |
| `host`        | IP address of the target machine.               |
| `macAddress`  | MAC address of the target machine.              |

#### frigate

| Parameter         | Description                                            |
| ----------------- | ------------------------------------------------------ |
| `name`            | Unique identifier for the frigate device.              |
| `timeoutMs`       | Timeout value in milliseconds for communication.       |
| `mqtt.host`       | IP address of the MQTT broker used by frigate.         |
| `mqtt.port`       | Port of the MQTT broker used by frigate. (default 1883)|
| `alert.url`       | URL for Pushover API, can be pointed at an [alert forwarder](#alert) or the official Pushover API |
| `alert.token`     | Pushover application token. (default "")               |
| `alert.user`      | Pushover user token. (default "")                      |
| `alert.priority`  | Priority level for the alert. (default 0)              |
| `frigate.url`     | URL for the Frigate service.                           |
| `frigate.externalUrl` | External URL for accessing Frigate. (default `frigate.url`) |
| `frigate.cacheEvents` | Cache clips from frigate events locally |
| `frigate.cachePath` | Path to cache frigate event clips to (default /tmp/cache) |

## Example

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
- type: frigate
  config:
    name: frigate
    timeoutMs: 3000
    mqtt:
      host: mosquitto.cluster.local
    alert:
      url: https://api.pushover.net/1/messages.json
      token: xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
      user: xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
    frigate:
        url: http://frigate.cluster.local
        externalUrl: https://frigate.example.com

```
