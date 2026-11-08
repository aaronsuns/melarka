# Marketing

Promo media for Melarka. Everything shown is the made-up demo library of
`web/e2e/demo/demo.py` (invented songs, generated tones and covers), never a real library.

`2026-11/`, the first round:

- `tools/promo.spec.ts`: Playwright captures of the phone UI, in Chinese and English. Copy it
  to `web/e2e/`, then from `web/`: `npm run build && LARK_SCREENSHOTS=1
  LARK_PROMO_DIR=$PWD/../marketing/2026-11/raw npx playwright test promo --project=iphone-webkit`
  (Node 22; delete the copy afterwards).
- `tools/build_video.py zh|en`: the vertical 1-minute videos, `melarka_{zh,en}.mp4`, with an
  ElevenLabs voice-over and a generated music bed. Needs Pillow and numpy, ffmpeg, and an
  ElevenLabs key in `~/.elevenlabs/key`. `VO_ONLY=1` makes only the voice lines;
  `tools/stt_check.py work/<lang>/vo.json` reads them back with speech-to-text, to catch
  misread words before rendering.
- `tools/build_cards.py`: the cover images in `cards/`.
- `texts/`: the post texts (Reddit, Hacker News, Facebook groups).

The English video: https://youtu.be/rkZQh5j8_ro
