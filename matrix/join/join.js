(function () {
  var PAGE_URL = "/join/page.json";
  var HS = "https://chat.sax.lgbt";
  var BOOT_ERR = "Page text didn't load. Reload in a minute.";

  function el(id) {
    var root = document.getElementById("join-root");
    if (root && id !== "join-root" && id !== "page-tpl") {
      var scoped = root.querySelector("#" + CSS.escape(id));
      if (scoped) return scoped;
    }
    return document.getElementById(id);
  }

  function mountPage() {
    var tpl = el("page-tpl");
    var root = el("join-root");
    root.innerHTML = "";
    root.appendChild(tpl.content.cloneNode(true));
    while (tpl.content.firstChild) {
      tpl.content.removeChild(tpl.content.firstChild);
    }
    if (tpl.parentNode) tpl.parentNode.removeChild(tpl);
    root.hidden = false;
  }

  function labels(page) {
    return page.shell || {};
  }

  function artBg(node, url) {
    if (!node || !url) return false;
    node.style.backgroundImage = "url(" + JSON.stringify(String(url)) + ")";
    node.hidden = false;
    return true;
  }

  function blockDrag(node) {
    if (!node || node.dataset.nosave) return;
    node.dataset.nosave = "1";
    node.addEventListener("contextmenu", function (e) {
      e.preventDefault();
    });
    node.addEventListener("dragstart", function (e) {
      e.preventDefault();
    });
  }

  function setupArt(page) {
    var waitArt = el("wait-art");
    var spinner = el("wait-spinner");
    var panel = el("waiting");
    if (artBg(waitArt, page.waiting_gif_url)) {
      blockDrag(waitArt);
      if (spinner) spinner.hidden = true;
      if (panel) panel.classList.add("has-gif");
    }
    var heroArt = el("invite-hero-art");
    if (artBg(heroArt, page.approved_image_url)) {
      blockDrag(heroArt);
    }
  }

  function setPhase(phase) {
    var banner = el("site-banner");
    if (phase === "form" || phase === "waiting" || phase === "invite") {
      if (banner) {
        banner.hidden = false;
        banner.classList.remove("site-banner--out");
      }
    }
    if (phase === "invite") {
      var hero = el("invite-hero");
      var heroArt = el("invite-hero-art");
      if (hero && heroArt && heroArt.style.backgroundImage) {
        hero.hidden = false;
      }
    }
  }

  function fillPage(page) {
    var sh = labels(page);
    document.title = (page.intro && page.intro.title) || sh.default_title || "chat.sax.lgbt";
    HS = page.homeserver_url || HS;
    setupArt(page);
    setPhase("form");
    el("mxid-label").textContent = sh.mxid_label || "";
    el("label-username").textContent = sh.label_username || "";
    el("label-email").textContent = sh.label_email || "";
    el("label-display").textContent = sh.label_display_name || "";
    el("label-token").textContent = sh.label_token || "";
    el("copy-token").textContent = sh.copy_token || "";
    var banner = el("join-banner");
    var bannerWrap = el("site-banner");
    if (banner && page.banner_url) {
      if (artBg(banner, page.banner_url)) {
        blockDrag(banner);
        if (bannerWrap) {
          blockDrag(bannerWrap);
          bannerWrap.hidden = false;
        }
      } else if (bannerWrap) {
        bannerWrap.hidden = true;
      }
    }
    el("intro-title").textContent = (page.intro && page.intro.title) || "";
    el("intro-lead").innerHTML = (page.intro && page.intro.lead_html) || "";
    var corridorJoin = el("corridor-join");
    if (corridorJoin) {
      var cj = page.corridor_join;
      if (cj && cj.html) {
        corridorJoin.innerHTML = cj.html;
        corridorJoin.hidden = false;
      } else {
        corridorJoin.innerHTML = "";
        corridorJoin.hidden = true;
      }
    }
    el("request-title").textContent = (page.request && page.request.title) || "";
    el("request-lead").innerHTML = (page.request && page.request.lead_html) || "";
    el("req-submit").textContent = (page.request && page.request.submit) || "";
    el("email-hint").textContent = sh.email_hint_optional || "";
    el("display-hint").textContent = sh.display_hint_optional || "";
    el("invite-title").textContent = (page.invite && page.invite.title) || "";
    var nextSteps = el("invite-next-steps");
    if (nextSteps) {
      var ns = page.invite && page.invite.next_steps_html;
      if (ns) {
        nextSteps.innerHTML = ns;
        nextSteps.hidden = false;
      } else {
        nextSteps.innerHTML = "";
        nextSteps.hidden = true;
      }
    }
    boot(page);
  }

  function boot(page) {
    var sh = labels(page);
    var serverName = (page.homeserver_url || HS).replace(/^https?:\/\//, "");
    var params = new URLSearchParams(location.search);
    var reqUser = el("req-user");
    var reqForm = el("req-form");
    var reqSubmit = el("req-submit");
    var formErr = el("form-err");
    var preview = el("mxid-preview");
    var waitingEl = el("waiting");
    var pollTimer = null;
    var pollSession = null;
    var SS_KEY = "mj_wait";
    var SS_SOURCE = "mj_src";
    var sourceInput = el("req-source");
    var joinSource = (params.get("source") || "").trim().toLowerCase();
    if (!joinSource) {
      try {
        var storedSource = sessionStorage.getItem(SS_SOURCE) || "";
        if (/^[a-z0-9-]{1,32}$/.test(storedSource)) joinSource = storedSource;
      } catch (e) {}
    }
    function applyJoinSource() {
      if (joinSource && /^[a-z0-9-]{1,32}$/.test(joinSource)) {
        sourceInput.value = joinSource;
        try {
          sessionStorage.setItem(SS_SOURCE, joinSource);
        } catch (e) {}
      }
      if (joinSource === "discord-migration" && page.discord) {
        el("intro-title").textContent = page.discord.intro.title;
        el("intro-lead").innerHTML = page.discord.intro.lead_html;
        el("request-title").textContent = page.discord.request.title;
        el("request-lead").innerHTML = page.discord.request.lead_html;
        el("email-hint").textContent = page.discord.request.email_hint;
        el("display-hint").textContent = page.discord.request.display_hint;
        el("req-submit").textContent = page.discord.request.submit;
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
      document.body.classList.add("join-invite");
      setPhase("invite");
      el("request").hidden = true;
      el("waiting").hidden = true;
      var introBody = el("intro-body");
      if (introBody) introBody.hidden = true;
      el("invite").hidden = false;
      var hero = el("invite-hero");
      var heroArt = el("invite-hero-art");
      if (hero && heroArt && heroArt.style.backgroundImage) {
        hero.hidden = false;
      }
      el("invite-token").value = token;
      el("invite-mxid").textContent = "@" + user + ":" + (server || serverName);
      var hs = page.homeserver_url || HS;
      var regLink = el("invite-register-link");
      if (regLink) {
        regLink.href =
          "https://app.element.io/#/register?hs_url=" + encodeURIComponent(hs.replace(/\/$/, ""));
      }
      el("copy-token").onclick = function () {
        navigator.clipboard.writeText(token);
      };
    }
    function waitingHint(hasEmail) {
      return hasEmail ? page.waiting.hint_with_email : page.waiting.hint_no_email;
    }
    function setWaitingMode(on) {
      document.body.classList.toggle("join-waiting", on);
    }
    function showSending() {
      setWaitingMode(true);
      setPhase("waiting");
      el("request").hidden = true;
      el("invite").hidden = true;
      var introBody = el("intro-body");
      if (introBody) introBody.hidden = true;
      waitingEl.hidden = false;
      waitingEl.dataset.rid = "";
      el("waiting-title").textContent = page.waiting.sending_title;
      el("waiting-hint").textContent = page.waiting.sending_lead || "";
      el("waiting-tick").textContent = "";
      var spinner = el("wait-spinner");
      if (spinner) spinner.hidden = false;
    }
    function showWaiting(rid, hasEmail) {
      setWaitingMode(true);
      setPhase("waiting");
      el("request").hidden = true;
      el("invite").hidden = true;
      var introBody = el("intro-body");
      if (introBody) introBody.hidden = true;
      waitingEl.hidden = false;
      waitingEl.dataset.rid = rid;
      el("waiting-title").textContent = page.waiting.title;
      el("waiting-hint").textContent = waitingHint(hasEmail);
      el("waiting-tick").textContent = page.waiting.tick;
      var spinner = el("wait-spinner");
      if (spinner) spinner.hidden = true;
    }
    function resetToForm() {
      stopPolling();
      clearWaitSession();
      setWaitingMode(false);
      document.body.classList.remove("join-invite");
      setPhase("form");
      var hero = el("invite-hero");
      if (hero) hero.hidden = true;
      waitingEl.hidden = true;
      waitingEl.dataset.rid = "";
      var introBody = el("intro-body");
      if (introBody) introBody.hidden = false;
      el("request").hidden = false;
    }
    function showFormError(msg) {
      formErr.textContent = msg;
      formErr.hidden = false;
      reqSubmit.disabled = false;
      reqSubmit.textContent =
        joinSource === "discord-migration" ? page.discord.request.submit : page.request.submit;
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
              var statusFmt =
                (page.errors && page.errors.status_check_failed) || sh.status_check_failed || "";
              throw new Error(
                r.status === 403 ? page.errors.wait_invalid : statusFmt.replace("%s", String(r.status))
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
              showFormError(page.errors.expired);
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
        showFormError(page.errors.username_required);
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
              throw new Error(
                t && t.length < 200 ? t : page.errors.request_failed + " (" + res.status + ")."
              );
            });
          }
          return res.json();
        })
        .then(function (d) {
          if (!d.rid || !d.wsig) throw new Error(page.errors.unexpected_response);
          var q = "rid=" + encodeURIComponent(d.rid) + "&wsig=" + encodeURIComponent(d.wsig);
          if (d.mail === "1") q += "&mail=1";
          history.replaceState(null, "", "/join?" + q);
          startPolling(d.rid, d.wsig, hasEmail || d.mail === "1");
        })
        .catch(function (err) {
          resetToForm();
          showFormError(err.message || page.errors.request_failed);
        });
    });
  }

  fetch(PAGE_URL, { cache: "no-store" })
    .then(function (r) {
      if (!r.ok) throw new Error("load");
      return r.json();
    })
    .then(function (page) {
      mountPage();
      fillPage(page);
    })
    .catch(function () {
      var root = el("join-root");
      if (root) root.hidden = false;
      var errEl = el("boot-msg");
      if (errEl) {
        errEl.textContent = BOOT_ERR;
        errEl.hidden = false;
      }
    });
})();
