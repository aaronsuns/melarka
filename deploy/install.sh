#!/bin/sh
# Melarka one-command installer (and upgrader). On a Linux host with Docker:
#
#   curl -fsSL https://raw.githubusercontent.com/aaronsuns/melarka/main/deploy/install.sh | sh
#
# or, to use a music folder you already have (Melarka only reads it):
#
#   curl -fsSL https://raw.githubusercontent.com/aaronsuns/melarka/main/deploy/install.sh | sh -s -- /path/to/your/music
#
# Without a path, the music folder is "music" inside the install directory. It makes an install directory, downloads docker-compose.yml and config.yaml next to a new .env,
# creates the writable directories for the container user (uid 1000), starts Melarka and prints
# how to sign in. Run it again later to upgrade: existing files and the database are kept.
#
# Optional environment variables:
#   MELARKA_DIR    install directory (default: /srv/melarka as root, otherwise ~/melarka)
#   LARK_LANGUAGE  default UI language for a new install: en, zh-Hans, zh-Hant or sv
#   MELARKA_RAW    where to download the files from (default: this repository's deploy/ on main)
set -eu

RAW=${MELARKA_RAW:-https://raw.githubusercontent.com/aaronsuns/melarka/main/deploy}
UID_IN_CONTAINER=1000

say() { printf '\033[1m==> %s\033[0m\n' "$*"; }
die() { printf '\033[31mError: %s\033[0m\n' "$*" >&2; exit 1; }

# Run a command as root: directly when we are root, otherwise through sudo.
as_root() {
	if [ "$(id -u)" -eq 0 ]; then "$@"; else sudo "$@"; fi
}

# --- inputs --------------------------------------------------------------------------------------

if [ -z "${MELARKA_DIR:-}" ]; then
	if [ "$(id -u)" -eq 0 ]; then MELARKA_DIR=/srv/melarka; else MELARKA_DIR=$HOME/melarka; fi
fi

# The music directory: the first argument, else the one an earlier install saved in .env, else
# "music" inside the install directory (created below).
MUSIC_ARG=${1:-}
MUSIC_DIR=$MUSIC_ARG
if [ -z "$MUSIC_DIR" ] && [ -f "$MELARKA_DIR/.env" ]; then
	MUSIC_DIR=$(sed -n 's/^MUSIC_DIR=//p' "$MELARKA_DIR/.env")
fi
DEFAULT_MUSIC=
if [ -z "$MUSIC_DIR" ]; then
	DEFAULT_MUSIC=1
	MUSIC_DIR=$MELARKA_DIR/music
else
	[ -d "$MUSIC_DIR" ] || die "music directory not found: $MUSIC_DIR"
	MUSIC_DIR=$(cd "$MUSIC_DIR" && pwd)
fi

# --- prerequisites -------------------------------------------------------------------------------

command -v curl >/dev/null 2>&1 || die "curl is required"
command -v docker >/dev/null 2>&1 || die "Docker is required: https://docs.docker.com/engine/install/"

DOCKER=docker
if ! docker info >/dev/null 2>&1; then
	if [ "$(id -u)" -ne 0 ] && sudo docker info >/dev/null 2>&1; then
		DOCKER="sudo docker"
	else
		die "Docker is installed but not running, or you are not allowed to use it"
	fi
fi
$DOCKER compose version >/dev/null 2>&1 || die "Docker Compose v2 is required (the 'docker compose' plugin)"

# --- files ---------------------------------------------------------------------------------------

say "Installing Melarka in $MELARKA_DIR"
if ! mkdir -p "$MELARKA_DIR" 2>/dev/null; then
	as_root mkdir -p "$MELARKA_DIR"
	as_root chown "$(id -u):$(id -g)" "$MELARKA_DIR"
fi
cd "$MELARKA_DIR"

fetch() { # fetch <file in deploy/> <local name>: only when the local file does not exist yet
	if [ -e "$2" ]; then
		echo "    keeping your $2"
	else
		curl -fsSL -o "$2" "$RAW/$1" || die "could not download $RAW/$1"
		echo "    downloaded $2"
	fi
}
fetch docker-compose.example.yml docker-compose.yml
fetch config.example.yaml config.yaml

NEW_ENV=
if [ -e .env ]; then
	echo "    keeping your .env"
	# A music folder given on the command line replaces the one saved by an earlier install.
	if [ -n "$MUSIC_ARG" ] && [ "$(sed -n 's/^MUSIC_DIR=//p' .env)" != "$MUSIC_DIR" ]; then
		(umask 077; grep -v '^MUSIC_DIR=' .env >.env.new || true; echo "MUSIC_DIR=$MUSIC_DIR" >>.env.new)
		mv .env.new .env
		echo "    music folder is now $MUSIC_DIR"
	fi
else
	if command -v openssl >/dev/null 2>&1; then
		PASSWORD=$(openssl rand -base64 18)
	else
		PASSWORD=$(head -c 18 /dev/urandom | base64)
	fi
	(
		umask 077
		{
			echo "MUSIC_DIR=$MUSIC_DIR"
			echo "LARK_ADMIN_PASSWORD=$PASSWORD"
			[ -z "${LARK_LANGUAGE:-}" ] || echo "LARK_LANGUAGE=$LARK_LANGUAGE"
		} >.env
	)
	NEW_ENV=1
	echo "    created .env with a new admin password"
fi

WRITABLE="data youtube channels previews"
if [ -n "$DEFAULT_MUSIC" ] && [ ! -d "$MUSIC_DIR" ]; then
	# Melarka only reads it; as root, give it to the container user, otherwise keep it yours so
	# you can copy music in without sudo.
	if [ "$(id -u)" -eq 0 ]; then WRITABLE="$WRITABLE music"; else (umask 022; mkdir -p music); fi
	echo "    no music folder given: created $MUSIC_DIR"
fi
# shellcheck disable=SC2086 # WRITABLE is a list of plain directory names
mkdir -p $WRITABLE
if [ "$(id -u)" -ne "$UID_IN_CONTAINER" ]; then
	# shellcheck disable=SC2086
	as_root chown "$UID_IN_CONTAINER:$UID_IN_CONTAINER" $WRITABLE
fi

# --- start ---------------------------------------------------------------------------------------

say "Downloading and starting Melarka"
$DOCKER compose pull --quiet
$DOCKER compose up -d

IMAGE=$($DOCKER compose config --images | head -1)
if ! $DOCKER run --rm -u "$UID_IN_CONTAINER:$UID_IN_CONTAINER" -v "$MUSIC_DIR:/m:ro" --entrypoint ls "$IMAGE" /m >/dev/null 2>&1; then
	printf '\033[33mWarning: Melarka (uid %s) cannot read %s. Make it readable, for example:\n  sudo chmod -R o+rX %s\033[0m\n' \
		"$UID_IN_CONTAINER" "$MUSIC_DIR" "$MUSIC_DIR"
fi

say "Waiting for Melarka to answer"
PORT=$(sed -n 's/^MELARKA_PORT=//p' .env)
URL=http://127.0.0.1:${PORT:-4600}
i=0
until curl -fsS "$URL/api/v1/info" 2>/dev/null | grep -q '"name":"Melarka"'; do
	i=$((i + 1))
	if [ "$i" -ge 60 ]; then
		$DOCKER compose logs --tail 30 melarka >&2 || true
		die "Melarka did not start within 2 minutes (logs above)"
	fi
	sleep 2
done

if [ -n "$NEW_ENV" ]; then
	PASSWORD="$(sed -n 's/^LARK_ADMIN_PASSWORD=//p' .env)
            (also in $MELARKA_DIR/.env; change it after you sign in)"
else
	PASSWORD="the one you set (the first one is in $MELARKA_DIR/.env)"
fi
cat <<EOF

$(printf '\033[32m')Melarka is running.$(printf '\033[0m')

  Open      $URL   (on this server)
  User      admin
  Password  $PASSWORD

  Music     $MUSIC_DIR
            Copy music there any time, or download songs from YouTube in the app.
            To use another folder, run this installer again with its path at the end.

Next steps:
  - Change the admin password in Me -> Change password, and add your family in Admin -> Users.
  - To use Melarka from phones, put HTTPS in front of it (README: "Reverse proxy and HTTPS").
  - To try it from other computers at home first, add MELARKA_BIND=0.0.0.0 to .env
    and run: cd $MELARKA_DIR && $DOCKER compose up -d
  - To upgrade later, run this installer again.
EOF
