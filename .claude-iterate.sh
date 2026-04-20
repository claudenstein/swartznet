#!/bin/bash
# SwartzNet Continuous Iteration Script
# Runs Claude Code in a loop with logging

LOG="/home/kartofel/Claude/swartznet/.claude-iterate.log"
ITERATION=1
START_TIME=$(date +%s)

echo "=== SwartzNet Claude Code Iterator Started at $(date) ===" >> "$LOG"

while true; do
    echo "" >> "$LOG"
    echo "=== ITERATION $ITERATION — $(date) ===" >> "$LOG"
    
    # Run Claude Code
    cd ~/Claude/swartznet
    PATH="/usr/local/go/bin:$PATH" claude --print --permission-mode bypassPermissions \
        "Continue iterating on SwartzNet (Go BitTorrent client with DHT search).

PROJECT: ~/Claude/swartznet
GO: /usr/local/go/bin/go

CURRENT ITERATION: $ITERATION
ELAPSED: $(( ($(date +%s) - $START_TIME) / 60 )) minutes

YOUR TASK:
1. Run all tests: PATH='/usr/local/go/bin:$PATH' go test ./... -count=1 -short
2. If tests fail, fix them. If all pass, find the weakest test coverage area and add tests.
3. Make ONE incremental improvement (new test, bugfix, refactor, etc.)
4. Run tests again to verify nothing broke.
5. Git commit with descriptive message.

PRIORITY: test coverage > edge cases > performance > code quality

Always run tests before and after changes. Never break existing tests." >> "$LOG" 2>&1
    
    EXIT_CODE=$?
    echo "Iteration $ITERATION completed (exit: $EXIT_CODE) at $(date)" >> "$LOG"
    
    if [ $EXIT_CODE -eq 0 ]; then
        echo "Claude finished successfully, starting next iteration..." >> "$LOG"
    else
        echo "Claude exited with code $EXIT_CODE, retrying in 30s..." >> "$LOG"
        sleep 30
    fi
    
    ITERATION=$((ITERATION + 1))
    
    # Brief pause between iterations to avoid hammering
    sleep 10
done
