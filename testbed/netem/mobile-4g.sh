#!/bin/sh
# Simulates a mobile 4G connection: 10 Mbit/s with 80ms average
# RTT and 40ms jitter (high variance typical of mobile).
tc qdisc add dev eth0 root netem delay 40ms 20ms rate 10mbit
