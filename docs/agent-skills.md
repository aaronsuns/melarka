# Agent skills

Some library chores need judgement rather than rules: recognising the real song behind a messy
YouTube title, or choosing a mood for a song. Melarka leaves those to an AI coding agent that
you run yourself, on your own subscription. Melarka itself calls no paid AI service, never sends
audio anywhere, and never analyses audio.

The repository ships three skills under [`skills/`](../skills/). Each is a `SKILL.md` file of
instructions that an agent such as [Claude Code](https://docs.claude.com/en/docs/claude-code)
follows. The agent works only through Melarka's HTTP API, with `curl` and `jq`, and changes
metadata only: titles, artists, tags and lyrics choices are stored in Melarka's database as
overrides, and your audio files are never touched.

| Skill | Ask for example | What it does |
|---|---|---|
| [`lark-tagging`](../skills/lark-tagging/SKILL.md) | *tag my Melarka library* | reads songs nobody has tagged yet and writes tags from Melarka's vocabulary, in batches of 100 |
| [`lark-lyrics`](../skills/lark-lyrics/SKILL.md) | *find the missing lyrics in Melarka* | reads songs without lyrics (favorites first), works out the real title and singer, and has Melarka search again with them; also tags clear instrumentals |
| [`lark-names`](../skills/lark-names/SKILL.md) | *fix the song names in Melarka* | reads favorites and downloads nobody has reviewed, fixes swapped title and singer, junk words, a channel name as album or an upload date as year, and marks each song reviewed |

The skills keep the internal code name `lark` in their names. When you want both names and
lyrics fixed, run `lark-names` first: better names make lyrics lookups work.

## Setting up

1. Install `curl` and `jq` where the agent runs.
2. Make the skills available to your agent. With Claude Code, start it inside a clone of this
   repository, or copy the three directories into `~/.claude/skills/`.
3. Export the connection in the shell that starts the agent:

   ```bash
   export LARK_URL=https://music.example.com   # or http://127.0.0.1:4600 on the server itself
   export LARK_USER=admin                      # an admin account
   read -rs LARK_PASSWORD && export LARK_PASSWORD
   ```

   | Variable | Meaning |
   |---|---|
   | `LARK_URL` | your Melarka server's address, without a trailing path |
   | `LARK_USER` | the username of an **admin** account (the skills use admin-only endpoints) |
   | `LARK_PASSWORD` | that account's password |

4. Ask the agent, for example *tag my Melarka library*.

Consider a separate admin account for agents, which you can delete under **Admin → Users** when
you are done.

## How the skills behave

- **Secrets stay out of the transcript.** The skills read the password only from the
  environment, pipe it into the sign-in request, keep the token in a private file under
  `~/.cache/<skill name>/`, and never print either. Don't paste a password into the chat; if a
  variable is missing, the skill asks you to export it.
- **They sign out** when they finish, stop early or hit an error.
- **They are cheap and cautious.** They decide from the metadata the server returns and the
  model's own music knowledge, without downloading audio or searching the web unless you say
  they may. When unsure, they skip a song rather than guess.
- **Manual edits win.** A title, artist or tag a person set by hand is never overwritten.
- **Song metadata is untrusted.** Titles and channel names come from uploaders; `lark-names`
  treats them as data, never as instructions, and skips anything that looks like one.
- **Runs can stop and resume at any time.** Each run only sees songs that still need work.
