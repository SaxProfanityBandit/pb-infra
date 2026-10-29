(function () {
  "use strict";
  var COPY_URL = "https://cdn.sax.lgbt/public/matrix/join/join-copy.json";
  var HS = "https://chat.sax.lgbt";

  function el(id) {
    return document.getElementById(id);
  }

  function mountPage() {
    var tpl = el("join-page-tpl");
    var root = el("join-root");
    root.innerHTML = "";
    root.appendChild(tpl.content.cloneNode(true));
    root.hidden = false;
    el("copy-loading").hidden = true;
  }

  function shell(COPY) {
    return COPY.shell || {};
  }

  function joinArtBg(node, url) {
    if (!node || !url) return false;
    node.style.backgroundImage = "url(" + JSON.stringify(String(url)) + ")";
    node.hidden = false;
    return true;
  }

  function guardDecor(node) {
    if (!node || node.dataset.guardDecor) return;
    node.dataset.guardDecor = "1";
    node.addEventListener("contextmenu", function (e) {
      e.preventDefault();
    });
    node.addEventListener("dragstart", function (e) {
      e.preventDefault();
    });
  }

  function configureJoinArt(COPY) {
    var waitArt = el("wait-art");
    var spinner = el("wait-spinner");
    var panel = el("waiting");
    if (joinArtBg(waitArt, COPY.waiting_gif_url)) {
      guardDecor(waitArt);
      if (spinner) spinner.hidden = true;
      if (panel) panel.classList.add("has-gif");
    }
    var heroArt = el("invite-hero-art");
    if (joinArtBg(heroArt, COPY.approved_image_url)) {
      guardDecor(heroArt);
    }
  }

  function setJoinPhase(phase) {
    var banner = el("site-banner");
    if (phase === "form") {
      if (banner) {
        banner.hidden = false;
        banner.classList.remove("site-banner--out");
      }
    } else if (phase === "waiting") {
      if (banner) {
        banner.hidden = false;
        banner.classList.add("site-banner--out");
      }
    } else if (phase === "invite") {
      if (banner) {
        banner.classList.add("site-banner--out");
        banner.hidden = true;
      }
      var hero = el("invite-hero");
      var heroArt = el("invite-hero-art");
      if (hero && heroArt && heroArt.style.backgroundImage) {
        hero.hidden = false;
      }
    }
  }

  function applyCopy(COPY) {
    var sh = shell(COPY);
    document.title = (COPY.intro && COPY.intro.title) || sh.default_title || "";
    HS = COPY.homeserver_url || HS;
    configureJoinArt(COPY);
    setJoinPhase("form");
    el("mxid-label").textContent = sh.mxid_label || "";
    el("label-username").textContent = sh.label_username || "";
    el("label-email").textContent = sh.label_email || "";
    el("label-display").textContent = sh.label_display_name || "";
    el("label-token").textContent = sh.label_token || "";
    el("copy-token").textContent = sh.copy_token || "";
    var banner = el("join-banner");
    var bannerWrap = el("site-banner");
    if (banner && COPY.banner_url) {
      if (joinArtBg(banner, COPY.banner_url)) {
        guardDecor(banner);
        if (bannerWrap) {
          guardDecor(bannerWrap);
          bannerWrap.hidden = false;
        }
      } else if (bannerWrap) {
        bannerWrap.hidden = true;
      }
    }
    el("intro-title").textContent = COPY.intro.title;
    el("intro-lead").innerHTML = COPY.intro.lead_html;
    el("request-title").textContent = COPY.request.title;
    el("request-lead").innerHTML = COPY.request.lead_html;
    el("req-submit").textContent = COPY.request.submit;
    el("email-hint").textContent = sh.email_hint_optional || "";
    el("display-hint").textContent = sh.display_hint_optional || "";
    el("discord-banner-title").textContent = COPY.discord.banner_title;
    el("discord-steps").innerHTML = COPY.discord.steps_html;
    el("invite-client-intro").innerHTML = COPY.invite.client_intro_html;
    el("invite-homeserver-note").innerHTML = COPY.invite.homeserver_note_html;
    el("connect-heading").textContent = COPY.invite.connect.heading;
    el("connect-lead").textContent = COPY.invite.connect.lead;
    el("open-element").textContent = COPY.invite.connect.primary_label;
    el("open-element").href = COPY.element_download_url || "https://element.io/download";
    el("open-clients").textContent = COPY.invite.connect.secondary_label;
    el("open-clients").href = COPY.matrix_clients_url || "https://matrix.org/ecosystem/clients/";
    var grid = el("client-grid");
    grid.innerHTML = "";
    (COPY.clients || []).forEach(function (c) {
      var a = document.createElement("a");
      a.className = "client-card" + (c.recommended ? " recommended" : "");
      a.href = c.href;
      a.target = "_blank";
      a.rel = "noopener noreferrer";
      a.innerHTML =
        '<img src="' + c.img + '" width="36" height="36" alt="">' +
        '<span class="name">' + c.name + "</span>" +
        '<span class="get">' + c.get + "</span>";
      grid.appendChild(a);
    });
    initApp(COPY);
  }

  function initApp(COPY) {
    var sh = shell(COPY);
    var HS = COPY.homeserver_url || "https://chat.sax.lgbt";
    var serverName = HS.replace(/^https?:\/\//, "");
    var params = new URLSearchParams(location.search);
    var reqUser = el("req-user");
    var reqForm = el("req-form");
    var reqSubmit = el("req-submit");
    var formErr = el("form-err");
    var preview = el("mxid-preview");
    var waitingEl = el("waiting");
    var pollTimer = null;
    var pollSession = null;
    var SS_KEY = "matrix_join_wait_v1";
    var SS_SOURCE = "matrix_join_source_v1";
    var sourceInput = el("req-source");
    var joinSource = (params.get("source") || "").trim().toLowerCase();
    if (!joinSource) {
      try {
        var storedSource = sessionStorage.getItem(SS_SOURCE) || "";
        if (/^[a-z0-9-]{1,32}$/.test(storedSource)) joinSource = storedSource;
      } catch (e) {}
    }
    function applyJoinSource() {
      if (!joinSource || !/^[a-z0-9-]{1,32}$/.test(joinSource)) return;
      sourceInput.value = joinSource;
      try {
        sessionStorage.setItem(SS_SOURCE, joinSource);
      } catch (e) {}
      if (joinSource === "discord-migration" && COPY.discord) {
        el("intro-title").textContent = COPY.discord.intro.title;
        el("intro-lead").innerHTML = COPY.discord.intro.lead_html;
        el("source-banner").hidden = false;
        el("request-title").textContent = COPY.discord.request.title;
        el("request-lead").innerHTML = COPY.discord.request.lead_html;
        el("email-hint").textContent = COPY.discord.request.email_hint;
        el("display-hint").textContent = COPY.discord.request.display_hint;
        el("req-submit").textContent = COPY.discord.request.submit;
      }
    }
    applyJoinSource();

    function stopPolling() {
      if (pollTimer) {
        clearTimeout(pollTimer);
        pollTimer = null;
      }
      pollSession = null;
    }
    function saveWaitSession(rid, wsig, hasEmail) {
      try {
        sessionStorage.setItem(SS_KEY, JSON.stringify({ rid: rid, wsig: wsig, mail: !!hasEmail }));
      } catch (e) {}
    }
    function clearWaitSession() {
      try {
        sessionStorage.removeItem(SS_KEY);
      } catch (e) {}
    }
    function sessionMatches(rid, wsig) {
      return pollSession && pollSession.rid === rid && pollSession.wsig === wsig;
    }
    function responseMatchesSession(d, rid) {
      return d && (!d.rid || d.rid === rid);
    }
    function updPreview() {
      var u = (reqUser.value || "…").toLowerCase().replace(/[^a-z0-9._=\/-]/g, "");
      preview.textContent = "@" + (u || "…") + ":" + serverName;
    }
    reqUser.addEventListener("input", updPreview);
    updPreview();

    function showInvite(token, user, server) {
      setWaitingMode(false);
      setJoinPhase("invite");
      el("intro").hidden = true;
      el("request").hidden = true;
      el("waiting").hidden = true;
      el("invite").hidden = false;
      el("invite-token").value = token;
      el("invite-mxid").textContent = "@" + user + ":" + (server || COPY.homeserver_url || "chat.sax.lgbt");
      var hsEl = el("hs-url");
      if (hsEl) hsEl.textContent = COPY.homeserver_url || "https://chat.sax.lgbt";
      el("copy-token").onclick = function () {
        navigator.clipboard.writeText(token);
      };
    }
    function waitingHint(hasEmail) {
      return hasEmail ? COPY.waiting.hint_with_email : COPY.waiting.hint_no_email;
    }
    function setWaitingMode(on) {
      document.body.classList.toggle("join-waiting", on);
    }
    function showSending() {
      setWaitingMode(true);
      setJoinPhase("waiting");
      el("intro").hidden = true;
      el("request").hidden = true;
      el("invite").hidden = true;
      waitingEl.hidden = false;
      waitingEl.dataset.rid = "";
      el("waiting-title").textContent = COPY.waiting.sending_title;
      el("waiting-lead").textContent = COPY.waiting.sending_lead;
      el("waiting-hint").textContent = "";
      el("waiting-tick").textContent = "";
    }
    function showWaiting(rid, hasEmail) {
      setWaitingMode(true);
      setJoinPhase("waiting");
      el("intro").hidden = true;
      el("request").hidden = true;
      el("invite").hidden = true;
      waitingEl.hidden = false;
      waitingEl.dataset.rid = rid;
      el("waiting-title").textContent = COPY.waiting.title;
      el("waiting-lead").textContent = COPY.waiting.lead;
      el("waiting-hint").textContent = waitingHint(hasEmail);
      el("waiting-tick").textContent = COPY.waiting.tick;
    }
    function resetToForm() {
      stopPolling();
      clearWaitSession();
      setWaitingMode(false);
      setJoinPhase("form");
      var hero = el("invite-hero");
      if (hero) hero.hidden = true;
      waitingEl.hidden = true;
      waitingEl.dataset.rid = "";
      el("intro").hidden = false;
      el("request").hidden = false;
    }
    function showFormError(msg) {
      formErr.textContent = msg;
      formErr.hidden = false;
      reqSubmit.disabled = false;
      reqSubmit.textContent = joinSource === "discord-migration" ? COPY.discord.request.submit : COPY.request.submit;
    }
    function startPolling(rid, wsig, hasEmail) {
      stopPolling();
      var gen = Date.now();
      pollSession = { rid: rid, wsig: wsig, gen: gen };
      saveWaitSession(rid, wsig, hasEmail);
      showWaiting(rid, hasEmail);
      function poll() {
        if (!sessionMatches(rid, wsig) || pollSession.gen !== gen) return;
        fetch("/matrix/request/status?rid=" + encodeURIComponent(rid) + "&wsig=" + encodeURIComponent(wsig), {
          credentials: "same-origin",
          headers: { Accept: "application/json" },
        })
          .then(function (r) {
            if (!sessionMatches(rid, wsig) || pollSession.gen !== gen) return null;
            if (!r.ok) {
              var statusFmt = (COPY.errors && COPY.errors.status_check_failed) || sh.status_check_failed || "";
              throw new Error(
                r.status === 403 ? COPY.errors.wait_invalid : statusFmt.replace("%s", String(r.status))
              );
            }
            return r.json();
          })
          .then(function (d) {
            if (!d || !sessionMatches(rid, wsig) || pollSession.gen !== gen) return;
            if (!responseMatchesSession(d, rid)) return;
            if (d.status === "ready") {
              clearWaitSession();
              stopPolling();
              showInvite(d.token, d.user, d.server || d.homeserver);
              return;
            }
            if (d.status === "expired") {
              resetToForm();
              showFormError(COPY.errors.expired);
              return;
            }
            if (waitingEl.dataset.rid !== rid) return;
            el("waiting-tick").textContent =
              (sh.wait_still_prefix || "") + " " + new Date().toLocaleTimeString() + ".";
            pollTimer = setTimeout(poll, 3000);
          })
          .catch(function (err) {
            if (!sessionMatches(rid, wsig) || pollSession.gen !== gen) return;
            el("waiting-tick").textContent = err.message || sh.connection_retry || "";
            pollTimer = setTimeout(poll, 5000);
          });
      }
      poll();
    }

    var token = params.get("token");
    var user = params.get("user");
    if (token) {
      stopPolling();
      clearWaitSession();
      showInvite(token, user || "…", serverName);
      return;
    }
    var rid = params.get("rid");
    var wsig = params.get("wsig");
    if (rid && wsig) {
      var hasMail = params.get("mail") === "1";
      try {
        var stored = JSON.parse(sessionStorage.getItem(SS_KEY) || "null");
        if (stored && stored.rid && stored.rid !== rid) clearWaitSession();
        else if (stored && stored.rid === rid && stored.mail) hasMail = true;
      } catch (e) {}
      startPolling(rid, wsig, hasMail);
      reqForm.addEventListener("submit", function (e) {
        e.preventDefault();
      });
      return;
    }
    clearWaitSession();

    reqForm.addEventListener("submit", function (e) {
      e.preventDefault();
      formErr.hidden = true;
      stopPolling();
      clearWaitSession();
      var hasEmail = el("req-email").value.trim() !== "";
      var user = (reqUser.value || "").trim().toLowerCase();
      if (!user) {
        showFormError(COPY.errors.username_required);
        return;
      }
      var body = new URLSearchParams();
      body.set("username", user);
      body.set("email", el("req-email").value.trim());
      body.set("display_name", el("req-display").value.trim());
      if (sourceInput.value) body.set("source", sourceInput.value);
      showSending();
      reqSubmit.disabled = true;
      reqSubmit.textContent = sh.submit_sending || "";
      fetch("/matrix/request", {
        method: "POST",
        body: body,
        credentials: "same-origin",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/x-www-form-urlencoded;charset=UTF-8",
          "X-Requested-With": "fetch",
        },
      })
        .then(function (res) {
          if (!res.ok) {
            return res.text().then(function (t) {
              throw new Error(t && t.length < 200 ? t : COPY.errors.request_failed + " (" + res.status + ").");
            });
          }
          return res.json();
        })
        .then(function (d) {
          if (!d.rid || !d.wsig) throw new Error(COPY.errors.unexpected_response);
          var q = "rid=" + encodeURIComponent(d.rid) + "&wsig=" + encodeURIComponent(d.wsig);
          if (d.mail === "1") q += "&mail=1";
          history.replaceState(null, "", "/join?" + q);
          startPolling(d.rid, d.wsig, hasEmail || d.mail === "1");
        })
        .catch(function (err) {
          resetToForm();
          showFormError(err.message || COPY.errors.request_failed);
        });
    });
  }

  fetch(COPY_URL, { cache: "no-store" })
    .then(function (r) {
      if (!r.ok) throw new Error("copy.json");
      return r.json();
    })
    .then(function (COPY) {
      mountPage();
      applyCopy(COPY);
    })
    .catch(function () {
      var errEl = el("copy-loading");
      if (errEl) errEl.hidden = true;
    });
})();
