package main

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed static/*
var oauthStaticRoot embed.FS

var oauthStaticFS = mustOAuthStatic()

func mustOAuthStatic() fs.FS {
	sub, err := fs.Sub(oauthStaticRoot, "static")
	if err != nil {
		panic(err)
	}
	return sub
}

func (s *Server) oauthStatic(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		http.NotFound(w, r)
		return
	}
	b, err := fs.ReadFile(oauthStaticFS, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasSuffix(name, ".png"):
		w.Header().Set("Content-Type", "image/png")
	case strings.HasSuffix(name, ".jpg"), strings.HasSuffix(name, ".jpeg"):
		w.Header().Set("Content-Type", "image/jpeg")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}

// Shared Sagalore / WoW-flavoured chrome for OAuth HTML pages.
// Images load from /oauth/static (same origin). No external fonts or scripts.
const oauthPageCSS = `
  :root {
    --bg: #05080f;
    --ink: #f2efe6;
    --ink-muted: #c9c2b2;
    --ink-dim: #9a9284;
    --gold: #c9a24a;
    --gold-bright: #f0d078;
    --glow: rgba(212, 168, 75, 0.3);
    --serif: "Iowan Old Style", "Palatino Linotype", Palatino, "Book Antiqua", Georgia, serif;
  }
  * { box-sizing: border-box; }
  html, body { height: 100%; }
  body {
    margin: 0;
    min-height: 100%;
    color: #e4ded2;
    font: 16px/1.5 var(--serif);
    background: var(--bg);
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 1.75rem 1rem;
    position: relative;
    overflow-x: hidden;
  }
  .map {
    position: fixed;
    inset: 0;
    z-index: 0;
    pointer-events: none;
    background-color: #0a1628;
    background-image:
      radial-gradient(ellipse 62% 58% at 50% 46%, rgba(3, 9, 22, 0) 25%, rgba(3, 9, 22, 0.55) 62%, rgba(2, 6, 16, 0.94) 100%),
      linear-gradient(rgba(10, 26, 58, 0.42), rgba(10, 26, 58, 0.42)),
      url("/oauth/static/map-tile.jpg");
    background-repeat: no-repeat, no-repeat, repeat;
    background-position: center, center, center;
    background-size: auto, auto, 720px auto;
  }
  .stage {
    position: relative;
    z-index: 1;
    width: min(36rem, 100%);
  }
  .brand {
    text-align: center;
    margin-bottom: 0.15rem;
  }
  .brand .emblem {
    width: 80px;
    height: 80px;
    display: block;
    margin: 0 auto 0.1rem;
    filter: drop-shadow(0 0 18px var(--glow));
  }
  .brand .wordmark {
    width: min(260px, 68%);
    height: auto;
    display: block;
    margin: 0 auto;
    filter: drop-shadow(0 2px 0 rgba(0, 0, 0, 0.55)) drop-shadow(0 0 14px var(--glow));
  }
  .frame {
    /* 9-slice: corners stay sharp; only edges/center stretch as the box grows. */
    --frame-slice: 78;
    --frame-border: 3.35rem;
    position: relative;
    width: 100%;
    aspect-ratio: 1 / 1;
    margin: -0.15rem auto 0;
    padding: 0.55rem 1.5rem 0.7rem;
    border: var(--frame-border) solid transparent;
    border-image-source: url("/oauth/static/panel.png");
    border-image-slice: var(--frame-slice) fill;
    border-image-width: var(--frame-border);
    border-image-repeat: stretch;
    background: transparent;
    filter: drop-shadow(0 16px 36px rgba(0, 0, 0, 0.55));
    display: flex;
    flex-direction: column;
    justify-content: center;
  }
  .frame-inner {
    position: relative;
    z-index: 1;
    width: 100%;
  }
  h1 {
    margin: 0 0 0.15rem;
    font: normal 1.85rem/1.15 var(--serif);
    text-align: center;
    letter-spacing: 0.01em;
    background: linear-gradient(180deg, #ecc979 0%, #cf9f48 55%, #9c6f2c 100%);
    -webkit-background-clip: text;
    background-clip: text;
    color: transparent;
    filter: drop-shadow(0 2px 0 rgba(0, 0, 0, 0.8)) drop-shadow(0 0 12px rgba(201, 162, 74, 0.28));
  }
  .lead, .body, .return {
    margin: 0;
    text-align: center;
  }
  .lead {
    color: #e6e0d4;
    font-size: 1rem;
    font-weight: normal;
    margin: 0.7rem 0 0.9rem;
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.8);
  }
  .body {
    color: #dcd5c8;
    font-size: 0.95rem;
    font-weight: 400;
    margin: 0.85rem 0 0;
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.75);
  }
  .return {
    color: rgba(168, 184, 210, 0.58);
    font-size: 0.8rem;
    margin: 0.7rem 0 0;
    word-break: break-all;
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.55);
  }
  .return code {
    font: inherit;
    color: rgba(168, 184, 210, 0.62);
  }
  .divider {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 0.5rem;
    margin: 0.35rem 0 0.15rem;
    opacity: 0.9;
  }
  .divider .line {
    flex: 1;
    height: 1px;
    max-width: 6.5rem;
    background: linear-gradient(90deg, transparent, var(--gold), transparent);
  }
  .divider .gem {
    width: 8px;
    height: 8px;
    transform: rotate(45deg);
    background: linear-gradient(135deg, var(--gold-bright), #7a5a1c);
    box-shadow: 0 0 8px var(--glow);
  }
  .who {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    margin: 0 auto;
    width: 100%;
    aspect-ratio: 3.35 / 1;
    padding: 0.4rem 1.35rem 0.5rem;
    text-align: center;
    background: url("/oauth/static/banner.png") center / 100% 100% no-repeat;
    filter: drop-shadow(0 6px 14px rgba(0, 0, 0, 0.4));
  }
  .who strong {
    display: block;
    font-size: 0.66rem;
    font-weight: normal;
    letter-spacing: 0.18em;
    text-transform: uppercase;
    color: #c9c3b6;
    margin-bottom: 0.15rem;
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.85);
  }
  .who .tag {
    font-weight: normal;
    font-size: 1.05rem;
    letter-spacing: 0.01em;
    background: linear-gradient(180deg, #f0cf7e 0%, #c99a45 100%);
    -webkit-background-clip: text;
    background-clip: text;
    color: transparent;
    filter: drop-shadow(0 1px 0 rgba(0, 0, 0, 0.85));
    word-break: break-word;
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
    justify-content: center;
    margin-top: 1.15rem;
  }
  button, .btn {
    appearance: none;
    cursor: pointer;
    border: 0;
    background-color: transparent;
    background-repeat: no-repeat;
    background-position: center;
    background-size: 100% 100%;
    font: normal 1.12rem/1 var(--serif);
    letter-spacing: 0.05em;
    text-indent: 0.05em;
    text-decoration: none;
    width: 10.25rem;
    height: 2.5rem;
    padding: 0.16rem 0.5rem 0;
    transition: filter 0.12s ease, transform 0.12s ease;
    filter: drop-shadow(0 8px 14px rgba(0, 0, 0, 0.5));
  }
  button:hover, .btn:hover { filter: brightness(1.08) drop-shadow(0 8px 14px rgba(0, 0, 0, 0.5)); }
  button:active, .btn:active { transform: translateY(1px); }
  button.allow, .btn.allow {
    color: #2a1a08;
    background-image: url("/oauth/static/btn-allow.png");
    text-shadow: 0 1px 0 rgba(255, 236, 170, 0.3);
  }
  button.deny, .btn.deny {
    color: #f6ecec;
    background-image: url("/oauth/static/btn-deny.png");
    text-shadow: 0 1px 0 rgba(0, 0, 0, 0.45);
  }
  a.continue {
    color: var(--gold-bright);
    text-decoration: underline;
    text-underline-offset: 3px;
  }
  a.continue:hover { color: #fff2c4; }
  @media (max-width: 640px) {
    /* Keep a little screen margin, but let content use the panel width.
       A thick border-image-width was eating the interior (slice > gold rim). */
    body { padding: 1rem 0.7rem; }
    .stage { width: 100%; }
    .brand .emblem { width: 68px; height: 68px; }
    .brand .wordmark { width: min(220px, 72%); }
    .frame {
      --frame-border: 1.45rem;
      --frame-slice: 72;
      padding: 0.2rem 0.35rem 0.35rem;
      aspect-ratio: auto;
      min-height: 0;
    }
    .who {
      width: 100%;
      aspect-ratio: 3.25 / 1;
      padding: 0.35rem 1.65rem 0.45rem;
    }
    .lead, .body, .return { padding: 0 0.15rem; }
    .actions { gap: 0.55rem; }
    button, .btn {
      width: 9.25rem;
      height: 2.35rem;
      font-size: 1.02rem;
    }
  }
`

type consentView struct {
	ClientName  string
	Who         string
	RedirectURI string
	RequestID   string
	CSRF        string
}

type handoffView struct {
	Target template.URL
}

const oauthChromeOpen = `<div class="map" aria-hidden="true"></div>
<div class="stage">
  <div class="brand">
    <img class="emblem" src="/oauth/static/emblem.png" width="88" height="88" alt="">
    <img class="wordmark" src="/oauth/static/wordmark.png" width="280" height="73" alt="Sagalore">
  </div>
  <div class="frame">
    <div class="frame-inner">
`

const oauthChromeClose = `
    </div>
  </div>
</div>
`

var consentTmpl = template.Must(template.New("consent").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sagalore — Allow access</title>
<style>` + oauthPageCSS + `</style>
</head>
<body>
` + oauthChromeOpen + `
    <h1>Allow {{.ClientName}}?</h1>
    <div class="divider" aria-hidden="true"><span class="line"></span><span class="gem"></span><span class="line"></span></div>
    <p class="lead">{{.ClientName}} is requesting access to use your Battle.net account through Sagalore.</p>
    <div class="who"><strong>Signed in as</strong><span class="tag">{{.Who}}</span></div>
    <p class="body">Sagalore will only use your Battle.net ID to identify your character and connect quest progress between the addon and companion app. You can revoke access at any time.</p>
    <div class="divider" aria-hidden="true"><span class="line"></span><span class="gem"></span><span class="line"></span></div>
    <p class="return">After choosing, the browser will return to <code>{{.RedirectURI}}</code></p>
    <form method="post" action="/oauth/authorize">
      <input type="hidden" name="request" value="{{.RequestID}}">
      <input type="hidden" name="csrf" value="{{.CSRF}}">
      <div class="actions">
        <button class="allow" type="submit" name="decision" value="approve">Allow</button>
        <button class="deny" type="submit" name="decision" value="deny">Deny</button>
      </div>
    </form>
` + oauthChromeClose + `
</body>
</html>
`))

var handoffTmpl = template.Must(template.New("handoff").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="0;url={{.Target}}">
<title>Sagalore</title>
<style>` + oauthPageCSS + `</style>
</head>
<body>
` + oauthChromeOpen + `
    <h1>Sending you back…</h1>
    <div class="divider" aria-hidden="true"><span class="line"></span><span class="gem"></span><span class="line"></span></div>
    <p class="body"><a class="continue" href="{{.Target}}">Continue</a> if the browser does not leave this page.</p>
` + oauthChromeClose + `
</body>
</html>
`))

var restartTmpl = template.Must(template.New("restart").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sagalore</title>
<style>` + oauthPageCSS + `</style>
</head>
<body>
` + oauthChromeOpen + `
    <h1>This sign-in is no longer available</h1>
    <div class="divider" aria-hidden="true"><span class="line"></span><span class="gem"></span><span class="line"></span></div>
    <p class="body">Go back to the app and start again.</p>
` + oauthChromeClose + `
</body>
</html>
`))
