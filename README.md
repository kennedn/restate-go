# restate-go
A cloudless home automation solution powered by Golang.

Currently supported device types:

|Device|Description|
|---|---|
|tvcom|Control LG5000 TVs over a [websocket serial bridge](https://github.com/kennedn/pico-ws-uart/)|
|meross|Control Meross bulb and sockets|
|wol|Control Wake-On-Lan enabled devices, power state can be toggled on devices utilising [Action-On-LAN](https://github.com/kennedn/Action-On-LAN)|
|snowdon| Control Snowdon II soundbars with the [Snowdon-II-wifi](https://github.com/kennedn/Snowdon-II-Wifi) mod
