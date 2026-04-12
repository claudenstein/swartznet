#!/bin/sh
# Simulates a fast LAN: 1 Gbit/s, 1ms RTT. The "happy path"
# baseline to measure against degraded profiles.
tc qdisc add dev eth0 root netem delay 0.5ms rate 1000mbit
