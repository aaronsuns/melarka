"""Speech-to-text every voice line (ElevenLabs scribe_v1) and print it next to the script."""
import json, os, sys, uuid, urllib.request
key = os.environ.get("ELEVEN_KEY") or open(os.path.expanduser("~/.elevenlabs/key")).read().strip()
for item in json.load(open(sys.argv[1])):
    b = uuid.uuid4().hex
    body = (f"--{b}\r\nContent-Disposition: form-data; name=\"model_id\"\r\n\r\nscribe_v1\r\n"
            f"--{b}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.mp3\"\r\nContent-Type: audio/mpeg\r\n\r\n").encode() \
        + open(item["mp3"], "rb").read() + f"\r\n--{b}--\r\n".encode()
    r = urllib.request.Request("https://api.elevenlabs.io/v1/speech-to-text", data=body, method="POST")
    r.add_header("xi-api-key", key); r.add_header("Content-Type", f"multipart/form-data; boundary={b}")
    print("SCRIPT:", item["vo"]); print("HEARD: ", json.loads(urllib.request.urlopen(r, timeout=120).read())["text"]); print()
