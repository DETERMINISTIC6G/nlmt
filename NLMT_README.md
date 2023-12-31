# Network Latency Measurement Tool

IRTT:
```
irtt client --tripm=oneway -i 10ms -f 5ms -g m1/fingolfin -l 500 -m 1 -d 20m -o d --outdir=/tmp/ 12.1.1.1
```

IRTTF:
```
irttf client --tripm=oneway -i 10ms -f 2ms -y 10.0 -p /dev/shm/5gue_frame -g m1/fingolfin -l 500 -m 1 -d 1m -o d --outdir=/tmp/ 12.1.1.1
```