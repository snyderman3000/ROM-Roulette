ROM ROULETTE for OnionOS (Miyoo Mini / Mini+)
=============================================
A slot machine for your ROM collection. Pull the lever, stop the reel,
and play whatever it lands on.

Created by Claude (Anthropic) for Keith Snyder.

CONTROLS
  The bottom bar always shows what the buttons do right now.

  Reel       A (or START) spin · X game card for the game on the payline
             Y favorite · L/R pool · Up/Down nudge · SELECT played & favorites
             B or MENU quit
  Spinning   A or B (or START) stop the reel
  Jackpot    the game card opens by itself after a second;
             A opens it right away · B stays on the reel
  Game card  A (or START) play · Y favorite · X spin again · B back
  Lists      A game card · Y favorite · L/R switch list · B back
  Anywhere   R2 sound on/off

POOLS
  All games, Favorites, Never played (from ROM Roulette), or any one system.

WHERE GAMES COME FROM
  Every emulator installed in /Emu (same as Onion's game list) — its rom
  folder and file types. Multi-disc .m3u sets and .cue/.bin pairs count once.
  Names come from miyoogamelist.xml / gamelist.xml when present, box art from
  each system's Imgs folder, and descriptions from a scraped gamelist.xml.

LAUNCHING
  Games start through Onion itself, exactly like the built-in Random Game app,
  so they show up in Recents, the GameSwitcher and Play Activity. When you
  quit the game you go back to Onion's main menu.

SOUND
  8-bit arcade effects — coin and lever pull, reel clicks that slow as it
  stops, brake, jackpot fanfare, menu blips and a power-up as the game starts.
  They're original chip-style sounds made for ROM Roulette (square, triangle
  and noise channels, like an 80s arcade board), and play at Onion's system
  volume. R2 turns them on or off; the choice is remembered. If something goes wrong
  with sound, data/sound.log says why.

YOUR DATA
  Played list, favorites and counts: App/ROMRoulette/data/state.json
