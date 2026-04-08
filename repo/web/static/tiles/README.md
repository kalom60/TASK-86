# Offline Map Tiles

The geospatial map (`/analytics/map`) uses Leaflet.js with locally-served XYZ
tiles at `/static/tiles/{z}/{x}/{y}.png`.  This directory is a placeholder —
tile files must be downloaded separately and placed here before the map will
render properly.

## Tile URL pattern

```
web/static/tiles/{z}/{x}/{y}.png
```

`z` = zoom level (0–18), `x`/`y` = tile column/row at that zoom.

## Option 1 — Download with the bundled script (recommended)

A helper script is provided at `scripts/download_tiles.sh`.  It fetches a
bounding-box tile set from OpenStreetMap's tile CDN at zoom levels 0–12,
which covers a region at a manageable file size (~50–200 MB depending on area).

```bash
# Adjust the bounding box for your district's region
bash scripts/download_tiles.sh \
  --min-lon -97.0 --max-lon -96.0 \
  --min-lat  32.5 --max-lat  33.5 \
  --min-zoom 0    --max-zoom 12
```

> **Usage policy**: OpenStreetMap tile CDN is for low-volume, non-production
> use only.  For production deployments, self-host tiles using an MBTiles
> bundle or a tile server such as [tileserver-gl](https://github.com/maptiler/tileserver-gl).

## Option 2 — MBTiles bundle (production)

1. Download a regional MBTiles file from [OpenMapTiles](https://openmaptiles.org/)
   or generate one with [Planetiler](https://github.com/onthegomap/planetiler).
2. Serve it with **tileserver-gl** or **Martin**.
3. Update `web/static/js/map.js` line 4 to point at the tile server URL
   instead of `/static/tiles/{z}/{x}/{y}.png`.

## Option 3 — Quick dev tile fetch (single command)

If you have `wget` installed, run:

```bash
bash scripts/download_tiles.sh --quick
```

This fetches a small default region (zoom 0–6 world overview, ~5 MB) sufficient
for development and demos.

## Directory structure after hydration

```
web/static/tiles/
  0/0/0.png
  1/0/0.png  1/0/1.png  1/1/0.png  1/1/1.png
  2/...
  ...
```

## Notes

- The map will silently skip any missing tile (see `errorTileUrl: ''` in `map.js`)
  so the application will run without tiles — the basemap simply appears blank.
- Tile files are excluded from version control via `.gitignore`
  (`web/static/tiles/**/*.png`).
