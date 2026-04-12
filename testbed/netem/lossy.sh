#!/bin/sh
# Simulates a lossy connection: 5% packet loss, 150ms RTT, moderate bandwidth.
# Tests how SwartzNet handles retransmissions and timeout tuning.
tc qdisc add dev eth0 root netem delay 75ms loss 5% rate 20mbit
