# Matter occupancy → Meross light experiment

This standalone experiment restores restate-go's already commissioned Matter
controller identity from `/tmp/state/matter`, establishes CASE directly to the
configured IPv4 address, and subscribes to:

```text
endpoint 1 / OccupancySensing (0x0406) / Occupancy (0x0000)
```

The first report immediately synchronizes the light. Each subsequent change
sends the equivalent of:

```bash
curl -X POST 'https://api.kennedn.com/v2/meross/office?code=toggle&value=${occupancy}'
```

Run it from the repository root:

```bash
go run ./experiments/matter-occupancy-light \
  -ip 192.168.1.167 \
  -device office \
  -endpoint 1 \
  -min-interval 0 \
  -max-interval 30
```

Those interval values are the defaults, so the shorter equivalent is:

```bash
go run ./experiments/matter-occupancy-light \
  -ip 192.168.1.167
```

If auto-detection finds zero or multiple state directories, specify the exact
directory:

```bash
go run ./experiments/matter-occupancy-light \
  -ip 192.168.1.167 \
  -state-dir /tmp/state/matter/office-5cc3f82838ba
```

The experiment does not commission or alter fabric material. Stop it with
Ctrl-C. It reconnects and resubscribes after session errors.
