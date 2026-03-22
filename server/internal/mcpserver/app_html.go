package mcpserver

// mediaResultsAppHTML is a self-contained HTML page that renders media search
// results as poster cards in a horizontal scrolling row. It implements the MCP
// Apps client protocol (JSON-RPC over postMessage) inline so no external SDK is
// needed. The visual style matches Cantinarr's dark dashboard theme.
const mediaResultsAppHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Cantinarr Media Results</title>
<style>
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
:root{
  --bg:#0F0A04;
  --surface:#1C1510;
  --surface-variant:#2A1F14;
  --accent:#E5A00D;
  --text-primary:#F0F0F0;
  --text-secondary:#9E918A;
  --border:#332818;
}
html,body{
  background:var(--bg);
  color:var(--text-primary);
  font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;
  overflow-x:hidden;
  min-height:0;
}
body{padding:12px 0}

.scroll-row{
  display:flex;
  gap:12px;
  padding:0 16px;
  overflow-x:auto;
  scroll-snap-type:x proximity;
  -webkit-overflow-scrolling:touch;
  scrollbar-width:thin;
  scrollbar-color:var(--surface-variant) transparent;
}
.scroll-row::-webkit-scrollbar{height:6px}
.scroll-row::-webkit-scrollbar-track{background:transparent}
.scroll-row::-webkit-scrollbar-thumb{background:var(--surface-variant);border-radius:3px}

.card{
  flex:0 0 120px;
  width:120px;
  scroll-snap-align:start;
  cursor:default;
}
.poster-wrap{
  position:relative;
  width:120px;
  height:180px;
  border-radius:10px;
  overflow:hidden;
  background:var(--surface-variant);
}
.poster-wrap img{
  width:100%;
  height:100%;
  object-fit:cover;
  display:block;
}
.poster-placeholder{
  width:100%;
  height:100%;
  display:flex;
  align-items:center;
  justify-content:center;
}
.poster-placeholder svg{
  width:32px;
  height:32px;
  fill:var(--text-secondary);
}
.rating-badge{
  position:absolute;
  top:6px;
  right:6px;
  padding:2px 6px;
  background:rgba(229,160,13,0.9);
  border-radius:6px;
  font-size:10px;
  font-weight:600;
  color:#fff;
  line-height:1.3;
}
.card-title{
  margin-top:6px;
  font-size:12px;
  font-weight:500;
  color:var(--text-primary);
  display:-webkit-box;
  -webkit-line-clamp:2;
  -webkit-box-orient:vertical;
  overflow:hidden;
  text-overflow:ellipsis;
  line-height:1.3;
}
.card-year{
  font-size:11px;
  color:var(--text-secondary);
  margin-top:2px;
}

.empty-state{
  padding:16px;
  text-align:center;
  color:var(--text-secondary);
  font-size:14px;
}

.loading{
  display:flex;
  gap:12px;
  padding:0 16px;
}
.shimmer{
  flex:0 0 120px;
  width:120px;
  height:180px;
  border-radius:10px;
  background:linear-gradient(90deg,var(--surface-variant) 25%,var(--surface) 50%,var(--surface-variant) 75%);
  background-size:200% 100%;
  animation:shimmer 1.5s infinite;
}
@keyframes shimmer{
  0%{background-position:200% 0}
  100%{background-position:-200% 0}
}
</style>
</head>
<body>
<div id="root">
  <div class="loading" id="loading">
    <div class="shimmer"></div>
    <div class="shimmer"></div>
    <div class="shimmer"></div>
    <div class="shimmer"></div>
    <div class="shimmer"></div>
    <div class="shimmer"></div>
  </div>
</div>
<script>
(function() {
  const TMDB_IMG = 'https://image.tmdb.org/t/p/w342';
  const root = document.getElementById('root');
  const loading = document.getElementById('loading');
  let initialized = false;
  let msgId = 1;

  // Movie icon SVG for missing posters
  const movieIconSVG = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M18 4l2 4h-3l-2-4h-2l2 4h-3l-2-4H8l2 4H7L5 4H4c-1.1 0-2 .9-2 2v12c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V4h-4z"/></svg>';

  function renderResults(results) {
    loading.style.display = 'none';

    if (!results || results.length === 0) {
      root.innerHTML = '<div class="empty-state">No results found.</div>';
      return;
    }

    const row = document.createElement('div');
    row.className = 'scroll-row';

    for (const item of results) {
      const card = document.createElement('div');
      card.className = 'card';

      const posterWrap = document.createElement('div');
      posterWrap.className = 'poster-wrap';

      if (item.poster_path) {
        const img = document.createElement('img');
        img.src = TMDB_IMG + item.poster_path;
        img.alt = item.title || '';
        img.loading = 'lazy';
        img.onerror = function() {
          this.parentNode.innerHTML = '<div class="poster-placeholder">' + movieIconSVG + '</div>';
        };
        posterWrap.appendChild(img);
      } else {
        posterWrap.innerHTML = '<div class="poster-placeholder">' + movieIconSVG + '</div>';
      }

      if (item.vote_average && item.vote_average > 0) {
        const badge = document.createElement('div');
        badge.className = 'rating-badge';
        badge.textContent = item.vote_average.toFixed(1);
        posterWrap.appendChild(badge);
      }

      card.appendChild(posterWrap);

      if (item.title) {
        const title = document.createElement('div');
        title.className = 'card-title';
        title.textContent = item.title;
        card.appendChild(title);
      }

      if (item.year) {
        const year = document.createElement('div');
        year.className = 'card-year';
        year.textContent = item.year;
        card.appendChild(year);
      }

      row.appendChild(card);
    }

    root.innerHTML = '';
    root.appendChild(row);
  }

  // MCP Apps postMessage protocol (JSON-RPC)
  function send(msg) {
    parent.postMessage(msg, '*');
  }

  function handleMessage(event) {
    let data = event.data;
    if (typeof data === 'string') {
      try { data = JSON.parse(data); } catch(e) { return; }
    }
    if (!data || !data.jsonrpc) return;

    // Handle initialize response
    if (data.id && data.result && !initialized) {
      initialized = true;
      return;
    }

    // Handle tool result notification (ui/toolResult)
    if (data.method === 'ui/toolResult') {
      const params = data.params || {};
      const content = params.content || params.result?.content || [];
      for (const c of content) {
        if (c.type === 'text' && c.text) {
          try {
            const envelope = JSON.parse(c.text);
            if (envelope.results) {
              renderResults(envelope.results);
              return;
            }
          } catch(e) {}
        }
      }
      return;
    }

    // Handle streamed tool input (ui/toolInput) - for streaming tool args
    if (data.method === 'ui/toolInput') {
      // We render on toolResult, not toolInput
      return;
    }
  }

  window.addEventListener('message', handleMessage);

  // Initialize connection with the host
  send({
    jsonrpc: '2.0',
    id: msgId++,
    method: 'ui/initialize',
    params: {
      appInfo: { name: 'Cantinarr Media Results', version: '1.0.0' },
      capabilities: {}
    }
  });
})();
</script>
</body>
</html>`
