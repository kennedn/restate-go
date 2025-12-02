# restate-go

A cloudless home automation solution powered by Golang. The service exposes REST endpoints for device control and MQTT listeners for background workflows.

Currently supported devices and listeners:

| Type | Description |
| --- | --- |
| alert | Forward messages to [Pushover](https://pushover.net/api#messages). |
| meross | Control Meross bulbs, sockets, switches, thermostats (MTS200B), and radiator valves (MSH300HK). |
| snowdon | Control Snowdon II soundbars with the [Snowdon-II-wifi](https://github.com/kennedn/Snowdon-II-Wifi) mod. |
| tvcom | Control LG5000 TVs over a [websocket serial bridge](https://github.com/kennedn/pico-ws-uart/). |
|wol|Control Wake-On-Lan enabled devices, power state can be toggled on devices utilising [Action-On-LAN](https://github.com/kennedn/Action-On-LAN)|
| bthome | Read BTHome sensor data via a [websocket bridge](https://github.com/kennedn/bthome-forwarder) |
| hikvision | Toggle IR/white-light modes and related features on Hikvision cameras. |
| frigate listener | Subscribe to Frigate MQTT review events, performs event backups and alerts to Pushover. |
| thermostat listener | Sync Meross radiator valves with central meross thermostat and BTHome temperature readings over MQTT. |

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

#### bthome

| Parameter | Description |
| ------------- | ------------------------------------------------ |
| `name` | Unique identifier for the BTHome sensor. |
| `macAddress` | MAC address of the BTHome device (colons allowed). |
| `host` | Websocket host broadcasting BTHome packets. |
| `timeout` | Timeout value in milliseconds for websocket reads. |

#### hikvision

| Parameter | Description |
| ------------- | ------------------------------------------------ |
| `name` | Unique identifier for the camera. |
| `timeoutMs` | Timeout value in milliseconds for communication. |
| `host` | IP address or hostname of the camera. |
| `user` | Camera username. |
| `password` | Camera password. |
| `defaultMode` | Optional default lighting mode (for example, `whiteLight`). |

#### meross (bulb, socket, switch)

| Parameter | Description |
| ------------- | ------------------------------------------------ |
| `name` | Unique identifier for the device. |
| `deviceType` | `bulb`, `socket`, or `switch`. |
| `timeoutMs` | Timeout value in milliseconds for communication. |
| `host` | IP address of the device. |
| `key` | Optional Meross device key. |

#### meross (thermostat: MTS200B)

| Parameter | Description |
| ------------- | ------------------------------------------------ |
| `name` | Unique identifier for the thermostat. |
| `deviceType` | `thermostat`. |
| `timeoutMs` | Timeout value in milliseconds for communication. |
| `host` | IP address of the device. |
| `key` | Optional Meross device key. |

#### meross (radiator valves via MSH300HK hub)

| Parameter     | Description                                      |
| `name` | Unique identifier for the hub. |
| `deviceType` | `radiator`. |
| `ids` | Array of valve IDs managed by the hub. |
| `timeoutMs` | Timeout value in milliseconds for communication. |
| `host` | IP address of the hub. |
| `key` | Optional Meross device key. |

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


#### frigate listener

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

#### thermostat listener

| Parameter | Description |
| ------------- | ------------------------------------------------ |
| `name` | Unique identifier for the listener. |
| `timeoutMs` | Timeout value in milliseconds for communication. |
| `mqtt.host` | IP address of the MQTT broker carrying BTHome messages. |
| `mqtt.port` | Port of the MQTT broker (default 1883). |
| `bthome.url` | Endpoint to query BTHome temperature readings. |
| `radiator.url` | Endpoint for Meross radiator control. |
| `radiator.uuid` | UUID identifying the Meross hub. |
| `thermostat.url` | Endpoint for the target thermostat. |
| `thermostat.uuid` | UUID identifying the thermostat. |
| `thermostat.syncIntervalMs` | Optional sync interval between thermostat and radiator state (default 15 minutes). |

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
- type: meross
  config:
    name: "thermostat"
    deviceType: thermostat
    timeoutMs: 2000
    host: "10.0.0.155"
- type: meross
  config:
    name: "radiators"
    deviceType: radiator
    timeoutMs: 2000
    host: "10.0.0.151"
    ids:
      - "0300C900"
      - "0500C901"
- type: bthome
  config:
    name: "living-room-sensor"
    macAddress: "AA:BB:CC:DD:EE:FF"
    host: "10.0.0.200:8080"
    timeout: 5000
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
- type: hikvision
  config:
    name: "driveway"
    timeoutMs: 1000
    host: "10.0.0.210"
    user: admin
    password: "********"
    defaultMode: whiteLight
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
        cacheEvents: false
- type: thermostat
  config:
    name: heating-sync
    timeoutMs: 3000
    mqtt:
      host: mosquitto.cluster.local
    bthome:
      url: http://localhost:8080/v2/bthome/living-room-sensor/status
    radiator:
      url: http://localhost:8080/v2/meross/radiators/status
      uuid: 0300C900
    thermostat:
      url: http://localhost:8080/v2/meross/thermostat/status
      uuid: 1A2B3C4D
      syncIntervalMs: 900000
```
