#!/usr/bin/env bash
# download_tiles.sh — fetch XYZ PNG tiles from OpenStreetMap into web/static/tiles/
#
# Usage:
#   bash scripts/download_tiles.sh [OPTIONS]
#
# Options:
#   --min-lon <float>   Western longitude  (default: -180)
#   --max-lon <float>   Eastern longitude  (default:  180)
#   --min-lat <float>   Southern latitude  (default:  -85)
#   --max-lat <float>   Northern latitude  (default:   85)
#   --min-zoom <int>    Minimum zoom level (default:    0)
#   --max-zoom <int>    Maximum zoom level (default:    8)
#   --quick             Fetch world overview only (zoom 0-6, ~5 MB)
#
# Requirements: bash, curl, bc, awk
#
# Tile server: https://tile.openstreetmap.org/{z}/{x}/{y}.png
# Rate limit: 1 request/second (OSM tile usage policy).
# For production use a self-hosted tile server — see tiles/README.md.

set -euo pipefail

TILE_DIR="$(dirname "$0")/../web/static/tiles"
TILE_URL="https://tile.openstreetmap.org"
DELAY=1  # seconds between requests (OSM policy)

# Defaults: whole world
MIN_LON=-180
MAX_LON=180
MIN_LAT=-85
MAX_LAT=85
MIN_ZOOM=0
MAX_ZOOM=8

# Parse arguments
while [[ $# -gt 0 ]]; do
  case "$1" in
    --quick)    MIN_LON=-180; MAX_LON=180; MIN_LAT=-85; MAX_LAT=85; MIN_ZOOM=0; MAX_ZOOM=6; shift ;;
    --min-lon)  MIN_LON="$2"; shift 2 ;;
    --max-lon)  MAX_LON="$2"; shift 2 ;;
    --min-lat)  MIN_LAT="$2"; shift 2 ;;
    --max-lat)  MAX_LAT="$2"; shift 2 ;;
    --min-zoom) MIN_ZOOM="$2"; shift 2 ;;
    --max-zoom) MAX_ZOOM="$2"; shift 2 ;;
    *) echo "Unknown option: $1" >&2; exit 1 ;;
  esac
done

# Convert lon/lat to tile x/y at a given zoom level.
# Uses awk for floating-point arithmetic.
lon_to_x() {
  local lon=$1 zoom=$2
  awk -v lon="$lon" -v z="$zoom" 'BEGIN {
    n = 2^z
    x = int((lon + 180) / 360 * n)
    print x
  }'
}

lat_to_y() {
  local lat=$1 zoom=$2
  awk -v lat="$lat" -v z="$zoom" 'BEGIN {
    pi = 3.14159265358979
    lat_r = lat * pi / 180
    n = 2^z
    y = int((1 - log(sin(lat_r) + 1/cos(lat_r)) / pi) / 2 * n)
    print y
  }'
}

mkdir -p "$TILE_DIR"

echo "Downloading tiles zoom=${MIN_ZOOM}-${MAX_ZOOM} into ${TILE_DIR}"
echo "Bounds: lon [${MIN_LON}, ${MAX_LON}]  lat [${MIN_LAT}, ${MAX_LAT}]"
echo "Rate-limited to 1 tile/second (OSM policy)."
echo ""

TOTAL=0
SKIP=0

for (( z=MIN_ZOOM; z<=MAX_ZOOM; z++ )); do
  x_min=$(lon_to_x "$MIN_LON" "$z")
  x_max=$(lon_to_x "$MAX_LON" "$z")
  y_min=$(lat_to_y "$MAX_LAT" "$z")   # note: lat_to_y is inverted (north = lower y)
  y_max=$(lat_to_y "$MIN_LAT" "$z")
  max_tile=$(( (1 << z) - 1 ))

  # Clamp to valid range
  [[ $x_min -lt 0 ]] && x_min=0
  [[ $y_min -lt 0 ]] && y_min=0
  [[ $x_max -gt $max_tile ]] && x_max=$max_tile
  [[ $y_max -gt $max_tile ]] && y_max=$max_tile

  for (( x=x_min; x<=x_max; x++ )); do
    for (( y=y_min; y<=y_max; y++ )); do
      dest="${TILE_DIR}/${z}/${x}/${y}.png"
      if [[ -f "$dest" ]]; then
        (( SKIP++ )) || true
        continue
      fi
      mkdir -p "$(dirname "$dest")"
      url="${TILE_URL}/${z}/${x}/${y}.png"
      if curl -sSf -o "$dest" \
              -H "User-Agent: DistrictPortal-TileFetcher/1.0 (on-prem)" \
              "$url"; then
        (( TOTAL++ )) || true
        printf "  z=%d x=%d y=%d  [%d fetched, %d skipped]\r" "$z" "$x" "$y" "$TOTAL" "$SKIP"
      else
        echo "  WARN: failed to fetch ${url}" >&2
        rm -f "$dest"
      fi
      sleep "$DELAY"
    done
  done
done

echo ""
echo "Done. ${TOTAL} tiles downloaded, ${SKIP} already present."
