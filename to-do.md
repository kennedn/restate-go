- Write tests for multi meross endpoints
- Write single endpoint test meross
- Fix all tests
- ~~Create mux wrapper for request logging~~


- ~~Re-write tvcom to use the config parsing model~~
- ~~Emit logs for config parsing?~~
- ~~Create a device interface under ./device/device.go and make each device implement the interface, this will allow a slice of devices to be created for route iteration~~

# Routes
- ~~remove all logic from Routes into generateRoutesFromConfig~~
- ~~rename generateRoutesFromConfig to routes~~
- ~~if only a single device found in config, do not add base routes, if more than one, add base routes and prepend base path to each device~~
- ~~Remove internalConfig param from public Routes func and move to private routes func if necessary~~
- ~~Add multi-device functionality and test to tvcom~~
- ~~Always continue in device loop, do not return error prematurely ~~