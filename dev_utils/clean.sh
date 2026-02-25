#!/bin/bash
# Kill soak process and clean up all state for a fresh start

echo "Killing soak processes..."
lsof -ti :14222 2>/dev/null | xargs kill 2>/dev/null

echo "Cleaning NATS data..."
rm -rf /tmp/soak /tmp/soak.port

echo "Cleaning prompt temp files..."
rm -f /tmp/soak-*.prompt

echo "Killing Claude agent tmux windows..."
tmux list-windows -a -F '#{session_name}:#{window_name}' 2>/dev/null | grep 'TICK-' | while read w; do
  tmux kill-window -t "${w}" 2>/dev/null
done

echo "Cleaning git worktrees..."
if [ -d .worktrees ]; then
    for wt in .worktrees/*/; do
        git worktree remove --force "$wt" 2>/dev/null
    done
    rmdir .worktrees 2>/dev/null
fi

echo "Cleaning ticket branches..."
git branch --list 'ticket/*' | while read branch; do
    git branch -D "$branch" 2>/dev/null
done

echo "Killing soak tmux session..."
tmux kill-session -t soak 2>/dev/null

echo "Done. Run ./soak to start fresh."
