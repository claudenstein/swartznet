#!/bin/sh
# Simulates a home DSL connection: 25 Mbit/s down, 5 Mbit/s up,
# 40ms average RTT with 10ms jitter.
tc qdisc add dev eth0 root netem delay 20ms 5ms rate 25mbit
