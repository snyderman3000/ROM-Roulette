#!/bin/sh
# ROM Roulette — spin the reel, play what it lands on
appdir=/mnt/SDCARD/App/ROMRoulette
sysdir=/mnt/SDCARD/.tmp_update
cd "$appdir" || exit 1
mkdir -p data
rm -f data/.launch

"$appdir/romroulette" > "$appdir/data/stdout.log" 2>&1
sync

# If a game was chosen, ROM Roulette has already written Onion's
# cmd_to_run.sh; quick_switch tells Onion to run it instead of going home.
if [ -f data/.launch ]; then
    rm -f data/.launch
    touch /tmp/quick_switch
fi
sync
