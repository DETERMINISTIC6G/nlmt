# Network Latency Measurement Tool

Use system's default clocks:
```
nlmt client --tripm=oneway -i 10ms -f 5ms -g m1/fingolfin -l 500 -m 1 -d 20m -o d --outdir=/tmp/ 12.1.1.1
```

Use a shared memory location as a clock:
```
nlmt client --tripm=oneway -i 10ms -f 2ms -p /dev/shm/5gue_frame -y 10.0 -g m1/fingolfin -l 500 -m 1 -d 1m -o d --outdir=/tmp/ 12.1.1.1
```