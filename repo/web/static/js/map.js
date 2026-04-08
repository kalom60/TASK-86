/**
 * map.js — District Materials Portal geospatial map
 *
 * Initialises a Leaflet map with an offline tile layer, loads GeoJSON from
 * /analytics/map/data, handles layer selection, renders a density grid as
 * coloured squares, and shows popups on location click.
 */

(function () {
  'use strict';

  // ----------------------------------------------------------------
  // Initialise Leaflet map with offline tile layer
  // ----------------------------------------------------------------
  var map = L.map('map', {
    center: [39.5, -98.35], // continental US centre — adjust for your district
    zoom: 6,
    zoomControl: true
  });

  L.tileLayer('/static/tiles/{z}/{x}/{y}.png', {
    attribution: 'District Materials Portal — offline tiles',
    maxZoom: 18,
    errorTileUrl: '' // silently skip missing tiles in offline mode
  }).addTo(map);

  // ----------------------------------------------------------------
  // Layer groups
  // ----------------------------------------------------------------
  var locationLayer = L.layerGroup().addTo(map);
  var densityLayer  = L.layerGroup().addTo(map);

  // ----------------------------------------------------------------
  // Marker colours by location type
  // ----------------------------------------------------------------
  var typeColour = {
    school:              '#4a6cf7',
    distribution_center: '#f59e0b',
    default:             '#6b7280'
  };

  function markerIcon(locType) {
    var colour = typeColour[locType] || typeColour['default'];
    var svgIcon = '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="36" viewBox="0 0 24 36">' +
      '<path fill="' + colour + '" stroke="#fff" stroke-width="1.5" d="M12 0C5.4 0 0 5.4 0 12c0 9 12 24 12 24s12-15 12-24C24 5.4 18.6 0 12 0z"/>' +
      '<circle fill="#fff" cx="12" cy="12" r="5"/>' +
      '</svg>';
    return L.divIcon({
      html: svgIcon,
      iconSize: [24, 36],
      iconAnchor: [12, 36],
      popupAnchor: [0, -36],
      className: ''
    });
  }

  // ----------------------------------------------------------------
  // Load and render GeoJSON from /analytics/map/data
  // ----------------------------------------------------------------
  function loadLayerData(layerType) {
    var statusEl = document.getElementById('mapStatus');
    if (statusEl) statusEl.textContent = 'Loading…';

    var url = '/analytics/map/data';
    if (layerType && layerType !== '') {
      url += '?layer=' + encodeURIComponent(layerType);
    }

    fetch(url)
      .then(function (res) {
        if (!res.ok) throw new Error('HTTP ' + res.status);
        return res.json();
      })
      .then(function (data) {
        renderLocations(data.geojson, data.locations);
        renderDensityGrid(data.aggregates);
        if (statusEl) statusEl.textContent = 'Loaded ' + (data.locations ? data.locations.length : 0) + ' location(s).';
      })
      .catch(function (err) {
        if (statusEl) statusEl.textContent = 'Error: ' + err.message;
        console.error('map.js: loadLayerData:', err);
      });
  }

  // ----------------------------------------------------------------
  // Render location markers from GeoJSON FeatureCollection string
  // ----------------------------------------------------------------
  function renderLocations(geoJSONStr, rawLocations) {
    locationLayer.clearLayers();
    if (!geoJSONStr) return;

    var fc;
    try {
      fc = typeof geoJSONStr === 'string' ? JSON.parse(geoJSONStr) : geoJSONStr;
    } catch (e) {
      console.error('map.js: invalid GeoJSON', e);
      return;
    }

    L.geoJSON(fc, {
      pointToLayer: function (feature, latlng) {
        var locType = (feature.properties && feature.properties.type) || 'default';
        return L.marker(latlng, { icon: markerIcon(locType) });
      },
      onEachFeature: function (feature, layer) {
        if (!feature.properties) return;
        var props = feature.properties;
        var rows = '';
        Object.keys(props).forEach(function (k) {
          if (k === 'id') return;
          rows += '<tr><th style="text-align:left;padding-right:8px">' + escapeHtml(k) + '</th>' +
                  '<td>' + escapeHtml(String(props[k])) + '</td></tr>';
        });
        var popup = '<strong>' + escapeHtml(props.name || 'Location') + '</strong>' +
                    '<table style="margin-top:6px;font-size:0.85rem">' + rows + '</table>';
        layer.bindPopup(popup);
      }
    }).addTo(locationLayer);
  }

  // ----------------------------------------------------------------
  // Render density grid as coloured squares
  // ----------------------------------------------------------------
  function renderDensityGrid(aggregates) {
    densityLayer.clearLayers();
    if (!aggregates || aggregates.length === 0) return;

    // Find max value for colour scaling
    var maxVal = 0;
    aggregates.forEach(function (agg) {
      var v = agg.value != null ? agg.value : 0;
      if (v > maxVal) maxVal = v;
    });
    if (maxVal === 0) maxVal = 1;

    aggregates.forEach(function (agg) {
      var cellKey = agg.cell_key || '';
      var parts = cellKey.split(',');
      if (parts.length < 2) return;

      var lat = parseFloat(parts[0]);
      var lng = parseFloat(parts[1]);
      if (isNaN(lat) || isNaN(lng)) return;

      var value = agg.value != null ? agg.value : 0;
      var intensity = value / maxVal; // 0..1
      var red   = Math.round(239 * intensity);
      var green = Math.round(68  * (1 - intensity));
      var blue  = Math.round(68  * (1 - intensity));
      var colour = 'rgb(' + red + ',' + green + ',' + blue + ')';

      // Approximate cell size in degrees (default ~10 km)
      var cellDeg = 0.09;

      var bounds = [
        [lat, lng],
        [lat + cellDeg, lng + cellDeg]
      ];

      var rect = L.rectangle(bounds, {
        color: colour,
        fillColor: colour,
        fillOpacity: 0.4 + 0.4 * intensity,
        weight: 0.5
      });

      rect.bindPopup(
        '<strong>Density Cell</strong><br>' +
        'Cell: ' + escapeHtml(cellKey) + '<br>' +
        'Metric: ' + escapeHtml(agg.metric || '') + '<br>' +
        'Value: ' + value
      );

      rect.addTo(densityLayer);
    });
  }

  // ----------------------------------------------------------------
  // Event handlers
  // ----------------------------------------------------------------
  var layerSelect    = document.getElementById('layerSelect');
  var loadLayerBtn   = document.getElementById('loadLayerBtn');
  var computeGridBtn = document.getElementById('computeGridBtn');
  var gridSizeInput  = document.getElementById('gridSizeInput');
  var metricInput    = document.getElementById('metricInput');
  var statusEl       = document.getElementById('mapStatus');

  if (loadLayerBtn) {
    loadLayerBtn.addEventListener('click', function () {
      var layer = layerSelect ? layerSelect.value : '';
      loadLayerData(layer);
    });
  }

  if (computeGridBtn) {
    computeGridBtn.addEventListener('click', function () {
      var layer    = layerSelect  ? layerSelect.value  : '';
      var gridSize = gridSizeInput ? gridSizeInput.value : '10';
      var metric   = metricInput   ? metricInput.value   : 'count';

      if (statusEl) statusEl.textContent = 'Computing grid…';

      var formData = new FormData();
      formData.append('layer',        layer);
      formData.append('grid_size_km', gridSize);
      formData.append('metric',       metric);

      fetch('/analytics/map/compute', { method: 'POST', body: formData })
        .then(function (res) { return res.json(); })
        .then(function (data) {
          if (data.status === 'ok') {
            if (statusEl) statusEl.textContent = 'Grid computed. Reloading layer…';
            loadLayerData(layer);
          } else {
            if (statusEl) statusEl.textContent = 'Error: ' + (data.error || 'unknown');
          }
        })
        .catch(function (err) {
          if (statusEl) statusEl.textContent = 'Error: ' + err.message;
          console.error('map.js: computeGrid:', err);
        });
    });
  }

  // Auto-load on page start
  loadLayerData('');

  // ----------------------------------------------------------------
  // Utility
  // ----------------------------------------------------------------
  function escapeHtml(str) {
    return String(str)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

})();
