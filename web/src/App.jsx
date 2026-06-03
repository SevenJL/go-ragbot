import { useEffect, useRef, useState } from "react";
import {
  Bot,
  BrainCircuit,
  FileStack,
  KeyRound,
  MessageSquare,
  PlugZap,
  Send,
  ShieldCheck,
  Sparkles,
  Upload,
  Wrench
} from "lucide-react";

const sessionId = `web-${Math.random().toString(36).slice(2)}`;

const starterMessage =
  "Hello. Upload a document to start retrieval, or try prompts like 'what time is it', 'calculate (3+4)*5', 'send an email', or 'check weather'.";

function clsx(...parts) {
  return parts.filter(Boolean).join(" ");
}

function formatCount(value, suffix = "") {
  if (value == null) {
    return "--";
  }
  return `${value}${suffix}`;
}

async function safeJSON(response) {
  const text = await response.text();
  if (!text) {
    return null;
  }
  try {
    return JSON.parse(text);
  } catch {
    return { raw: text };
  }
}

function sourceLabel(meta) {
  if (!meta) {
    return "";
  }
  return meta.skill_name ? `${meta.source}/${meta.skill_name}` : meta.source || "";
}

export default function App() {
  const [messages, setMessages] = useState([
    {
      id: crypto.randomUUID(),
      who: "bot",
      text: starterMessage,
      source: "system",
      streaming: false
    }
  ]);
  const [input, setInput] = useState("");
  const [docs, setDocs] = useState([]);
  const [plugins, setPlugins] = useState([]);
  const [skills, setSkills] = useState([]);
  const [health, setHealth] = useState(null);
  const [apiKey, setApiKey] = useState(() => localStorage.getItem("ragbot_api_key") || "");
  const [draftKey, setDraftKey] = useState(() => localStorage.getItem("ragbot_api_key") || "");
  const [showAuth, setShowAuth] = useState(false);
  const [sending, setSending] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [loadingPanel, setLoadingPanel] = useState(true);
  const [errorMessage, setErrorMessage] = useState("");
  const fileRef = useRef(null);
  const chatRef = useRef(null);

  useEffect(() => {
    void refreshAll();
  }, []);

  useEffect(() => {
    if (!chatRef.current) {
      return;
    }
    chatRef.current.scrollTop = chatRef.current.scrollHeight;
  }, [messages]);

  function authHeaders(headers = {}) {
    const next = new Headers(headers);
    const key = (localStorage.getItem("ragbot_api_key") || "").trim();
    if (key) {
      next.set("Authorization", `Bearer ${key}`);
    }
    return next;
  }

  async function apiFetch(url, options = {}, retried = false) {
    const response = await fetch(url, {
      ...options,
      headers: authHeaders(options.headers || {})
    });
    if (response.status === 401 && !retried) {
      setShowAuth(true);
    }
    return response;
  }

  function pushMessage(next) {
    const message = { id: crypto.randomUUID(), streaming: false, ...next };
    setMessages((current) => [...current, message]);
    return message.id;
  }

  function patchMessage(id, patch) {
    setMessages((current) =>
      current.map((message) => (message.id === id ? { ...message, ...patch } : message))
    );
  }

  async function refreshAll() {
    setLoadingPanel(true);
    setErrorMessage("");
    try {
      const [healthRes, docsRes, pluginsRes, skillsRes] = await Promise.all([
        apiFetch("/api/v1/health"),
        apiFetch("/api/v1/docs"),
        apiFetch("/api/v1/plugins"),
        apiFetch("/api/v1/skills")
      ]);

      const [healthBody, docsBody, pluginsBody, skillsBody] = await Promise.all([
        safeJSON(healthRes),
        safeJSON(docsRes),
        safeJSON(pluginsRes),
        safeJSON(skillsRes)
      ]);

      setHealth(healthRes.ok ? healthBody : null);
      setDocs(Array.isArray(docsBody) ? docsBody : []);
      setPlugins(Array.isArray(pluginsBody) ? pluginsBody : []);
      setSkills(Array.isArray(skillsBody) ? skillsBody : []);

      if (!healthRes.ok && healthBody?.error) {
        setErrorMessage(healthBody.error);
      }
    } catch (error) {
      setErrorMessage(`Failed to load control panels: ${String(error)}`);
    } finally {
      setLoadingPanel(false);
    }
  }

  async function sendMessage() {
    const text = input.trim();
    if (!text || sending) {
      return;
    }

    setInput("");
    pushMessage({ who: "user", text });

    const botId = pushMessage({
      who: "bot",
      text: "",
      source: "streaming",
      streaming: true
    });

    setSending(true);

    try {
      await streamChat(text, botId);
      void refreshAll();
    } catch (error) {
      patchMessage(botId, {
        text: `Request failed: ${String(error)}`,
        source: "error",
        streaming: false
      });
    } finally {
      setSending(false);
    }
  }

  async function streamChat(text, botId) {
    const response = await apiFetch("/api/v1/chat", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "text/event-stream"
      },
      body: JSON.stringify({
        session_id: sessionId,
        message: text,
        stream: true
      })
    });

    if (!response.ok) {
      const body = await safeJSON(response);
      throw new Error(body?.error || response.statusText || "Unknown error");
    }
    if (!response.body) {
      throw new Error("Streaming response body is unavailable");
    }

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    let answer = "";
    let meta = null;

    while (true) {
      const { value, done } = await reader.read();
      buffer += decoder.decode(value || new Uint8Array(), { stream: !done });

      let boundary = buffer.indexOf("\n\n");
      while (boundary !== -1) {
        const block = buffer.slice(0, boundary);
        buffer = buffer.slice(boundary + 2);
        const event = parseSSEBlock(block);

        if (event) {
          if (event.type === "message") {
            if (event.data === "[DONE]") {
              patchMessage(botId, {
                text: answer || "Completed without content.",
                source: sourceLabel(meta) || "rag",
                streaming: false
              });
              return;
            }
            answer += event.data;
            patchMessage(botId, {
              text: answer,
              source: sourceLabel(meta) || "streaming",
              streaming: true
            });
          }

          if (event.type === "meta") {
            meta = JSON.parse(event.data);
            patchMessage(botId, {
              source: sourceLabel(meta) || "rag"
            });
          }

          if (event.type === "error") {
            throw new Error(event.data || "Streaming failed");
          }
        }

        boundary = buffer.indexOf("\n\n");
      }

      if (done) {
        break;
      }
    }

    patchMessage(botId, {
      text: answer || "Completed without content.",
      source: sourceLabel(meta) || "rag",
      streaming: false
    });
  }

  async function uploadDocument() {
    const file = fileRef.current?.files?.[0];
    if (!file || uploading) {
      return;
    }

    const formData = new FormData();
    formData.append("file", file);
    setUploading(true);

    try {
      const response = await apiFetch("/api/v1/upload", {
        method: "POST",
        body: formData
      });
      const body = await safeJSON(response);
      if (!response.ok || body?.error) {
        pushMessage({
          who: "bot",
          text: `Upload failed: ${body?.error || response.statusText}`,
          source: "system"
        });
        return;
      }

      pushMessage({
        who: "bot",
        text: `Indexed ${body.filename} into ${body.chunks} chunks.`,
        source: "system"
      });
      if (fileRef.current) {
        fileRef.current.value = "";
      }
      await refreshAll();
    } catch (error) {
      pushMessage({
        who: "bot",
        text: `Upload failed: ${String(error)}`,
        source: "system"
      });
    } finally {
      setUploading(false);
    }
  }

  async function deleteDoc(docId) {
    try {
      const response = await apiFetch(`/api/v1/docs?id=${encodeURIComponent(docId)}`, {
        method: "DELETE"
      });
      const body = await safeJSON(response);
      if (!response.ok || body?.error) {
        setErrorMessage(body?.error || "Failed to delete document");
        return;
      }
      await refreshAll();
    } catch (error) {
      setErrorMessage(`Failed to delete document: ${String(error)}`);
    }
  }

  async function togglePlugin(name, enabled) {
    setPlugins((current) =>
      current.map((plugin) => (plugin.name === name ? { ...plugin, enabled } : plugin))
    );
    try {
      const response = await apiFetch("/api/v1/plugins/toggle", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name, enabled })
      });
      const body = await safeJSON(response);
      if (!response.ok || body?.error) {
        throw new Error(body?.error || response.statusText);
      }
    } catch (error) {
      setErrorMessage(`Failed to update plugin: ${String(error)}`);
      setPlugins((current) =>
        current.map((plugin) =>
          plugin.name === name ? { ...plugin, enabled: !enabled } : plugin
        )
      );
    }
  }

  function saveApiKey() {
    const trimmed = draftKey.trim();
    if (trimmed) {
      localStorage.setItem("ragbot_api_key", trimmed);
    } else {
      localStorage.removeItem("ragbot_api_key");
    }
    setApiKey(trimmed);
    setDraftKey(trimmed);
    setShowAuth(false);
    void refreshAll();
  }

  const statCards = [
    {
      label: "Knowledge Chunks",
      value: formatCount(health?.chunks),
      icon: FileStack,
      tone: "from-ember/25 via-ember/10 to-transparent"
    },
    {
      label: "Sessions",
      value: formatCount(health?.sessions),
      icon: MessageSquare,
      tone: "from-slate-400/30 via-slate-200/10 to-transparent"
    },
    {
      label: "Plugins",
      value: formatCount(health?.plugins),
      icon: PlugZap,
      tone: "from-ochre/35 via-ochre/10 to-transparent"
    },
    {
      label: "Skills",
      value: formatCount(health?.skills),
      icon: Wrench,
      tone: "from-ink/20 via-ink/5 to-transparent"
    }
  ];

  return (
    <div className="min-h-screen bg-parchment text-ink">
      <div className="pointer-events-none fixed inset-0 bg-[radial-gradient(circle_at_top_left,rgba(220,111,56,0.18),transparent_28%),radial-gradient(circle_at_bottom_right,rgba(228,177,95,0.16),transparent_26%)]" />
      <div className="pointer-events-none fixed inset-0 bg-grid bg-[size:42px_42px] opacity-40" />

      <div className="relative mx-auto flex min-h-screen max-w-[1700px] flex-col px-4 py-4 sm:px-6 lg:px-8">
        <header className="mb-4 rounded-[28px] border border-ink/10 bg-white/80 px-5 py-5 shadow-panel backdrop-blur md:px-7">
          <div className="flex flex-col gap-5 lg:flex-row lg:items-end lg:justify-between">
            <div className="max-w-3xl">
              <div className="mb-3 inline-flex items-center gap-2 rounded-full border border-ink/10 bg-ink px-3 py-1 text-[11px] font-semibold uppercase tracking-[0.28em] text-parchment">
                <Sparkles className="h-3.5 w-3.5" />
                Knowledge Console
              </div>
              <h1 className="font-display text-3xl font-bold tracking-tight sm:text-4xl">
                Operate retrieval, plugins, and skills from one desk
              </h1>
              <p className="mt-3 max-w-2xl text-sm leading-6 text-slate">
                This workspace is built for fast iteration: manage documents on the left, validate behavior in the center, and keep an eye on runtime state on the right.
              </p>
            </div>

            <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
              {statCards.map(({ label, value, icon: Icon, tone }) => (
                <div
                  key={label}
                  className={clsx(
                    "min-w-[128px] rounded-2xl border border-ink/10 bg-gradient-to-br p-4",
                    tone
                  )}
                >
                  <div className="mb-7 flex items-center justify-between">
                    <span className="text-xs uppercase tracking-[0.22em] text-slate">{label}</span>
                    <Icon className="h-4 w-4 text-ink/70" />
                  </div>
                  <div className="font-display text-3xl font-semibold">{value}</div>
                </div>
              ))}
            </div>
          </div>
        </header>

        <div className="grid flex-1 gap-4 xl:grid-cols-[340px_minmax(0,1fr)_320px]">
          <aside className="order-2 flex min-h-[420px] flex-col gap-4 xl:order-1">
            <Panel
              icon={Upload}
              title="Knowledge Base"
              subtitle="Upload source files, inspect indexed chunks, and remove stale content."
            >
              <label className="group block cursor-pointer rounded-2xl border border-dashed border-ink/20 bg-ink/5 p-4 transition hover:border-ember hover:bg-ember/5">
                <div className="flex items-center gap-3">
                  <div className="rounded-2xl bg-white p-3 shadow-sm">
                    <Upload className="h-5 w-5 text-ember" />
                  </div>
                  <div>
                    <div className="font-semibold">Drop or choose a file</div>
                    <div className="text-sm text-slate">PDF, TXT, and Markdown are supported</div>
                  </div>
                </div>
                <input
                  ref={fileRef}
                  type="file"
                  accept=".pdf,.txt,.md,.markdown"
                  className="sr-only"
                />
              </label>

              <button
                type="button"
                onClick={uploadDocument}
                disabled={uploading}
                className="mt-3 inline-flex w-full items-center justify-center gap-2 rounded-2xl bg-ink px-4 py-3 text-sm font-semibold text-parchment transition hover:bg-ink/90 disabled:cursor-not-allowed disabled:opacity-60"
              >
                <Upload className="h-4 w-4" />
                {uploading ? "Indexing..." : "Upload and Index"}
              </button>

              <div className="mt-4 space-y-3">
                {docs.length === 0 ? (
                  <EmptyState
                    title="No documents yet"
                    description="Upload one file first and the chat panel can start retrieving from it."
                  />
                ) : (
                  docs.map((doc) => (
                    <div
                      key={doc.id}
                      className="rounded-2xl border border-ink/10 bg-white/70 p-4 shadow-sm"
                    >
                      <div className="flex items-start justify-between gap-3">
                        <div className="min-w-0">
                          <div className="truncate font-semibold">{doc.source}</div>
                          <div className="mt-1 text-xs uppercase tracking-[0.22em] text-slate">
                            {doc.chunks} chunks
                          </div>
                        </div>
                        <button
                          type="button"
                          onClick={() => deleteDoc(doc.id)}
                          className="rounded-full border border-ink/10 px-3 py-1.5 text-xs font-semibold text-slate transition hover:border-ember hover:text-ember"
                        >
                          Delete
                        </button>
                      </div>
                    </div>
                  ))
                )}
              </div>
            </Panel>

            <Panel
              icon={PlugZap}
              title="Plugin Switches"
              subtitle="Toggle BeforeRAG and fallback behavior without restarting the server."
            >
              <div className="space-y-3">
                {plugins.length === 0 ? (
                  <EmptyState
                    title="No plugins"
                    description="There are no plugin records available right now."
                  />
                ) : (
                  plugins.map((plugin) => (
                    <div
                      key={plugin.name}
                      className="flex items-center justify-between gap-3 rounded-2xl border border-ink/10 bg-white/70 p-4"
                    >
                      <div className="min-w-0">
                        <div className="font-semibold">{plugin.name}</div>
                        <div className="mt-1 text-sm leading-5 text-slate">{plugin.description}</div>
                      </div>
                      <button
                        type="button"
                        onClick={() => togglePlugin(plugin.name, !plugin.enabled)}
                        className={clsx(
                          "relative h-8 w-14 rounded-full transition",
                          plugin.enabled ? "bg-ink" : "bg-ink/15"
                        )}
                        aria-label={`Toggle plugin ${plugin.name}`}
                        aria-pressed={plugin.enabled}
                      >
                        <span
                          className={clsx(
                            "absolute top-1 h-6 w-6 rounded-full bg-parchment transition",
                            plugin.enabled ? "left-7" : "left-1"
                          )}
                        />
                      </button>
                    </div>
                  ))
                )}
              </div>
            </Panel>
          </aside>

          <main className="order-1 flex min-h-[720px] flex-col rounded-[28px] border border-ink/10 bg-white/80 shadow-panel backdrop-blur xl:order-2">
            <div className="border-b border-ink/10 px-5 py-5 sm:px-6">
              <div className="flex flex-col gap-4 md:flex-row md:items-center md:justify-between">
                <div>
                  <div className="inline-flex items-center gap-2 rounded-full bg-ember/10 px-3 py-1 text-xs font-semibold uppercase tracking-[0.26em] text-ember">
                    <Bot className="h-3.5 w-3.5" />
                    Live Chat
                  </div>
                  <h2 className="mt-3 font-display text-2xl font-semibold">Streaming answer preview</h2>
                </div>
                <div className="flex flex-wrap gap-2">
                  <Chip icon={BrainCircuit} label={health?.embedder || "Embedder"} />
                  <Chip icon={Sparkles} label={health?.llm || "LLM"} />
                  <Chip icon={ShieldCheck} label={apiKey ? "Authenticated" : "Open mode"} />
                </div>
              </div>
            </div>

            <div
              ref={chatRef}
              className="flex-1 space-y-4 overflow-y-auto px-5 py-5 sm:px-6"
            >
              {messages.map((message) => (
                <article
                  key={message.id}
                  className={clsx(
                    "max-w-3xl rounded-[24px] border px-4 py-4 shadow-sm sm:px-5",
                    message.who === "user"
                      ? "ml-auto border-ink bg-ink text-parchment"
                      : "border-ink/10 bg-parchment/70 text-ink"
                  )}
                >
                  <div className="mb-2 flex items-center gap-2 text-xs uppercase tracking-[0.22em]">
                    <span>{message.who === "user" ? "You" : "RAG Bot"}</span>
                    {message.source ? (
                      <span
                        className={clsx(
                          "rounded-full px-2 py-0.5 text-[10px] tracking-[0.2em]",
                          message.who === "user"
                            ? "bg-parchment/15 text-fog"
                            : "bg-white/80 text-slate"
                        )}
                      >
                        {message.source}
                      </span>
                    ) : null}
                    {message.streaming ? (
                      <span className="rounded-full bg-ember/15 px-2 py-0.5 text-[10px] tracking-[0.2em] text-ember">
                        live
                      </span>
                    ) : null}
                  </div>
                  <div className="whitespace-pre-wrap text-sm leading-7 sm:text-[15px]">
                    {message.text || (message.streaming ? "..." : "")}
                  </div>
                </article>
              ))}
            </div>

            <div className="border-t border-ink/10 px-5 py-5 sm:px-6">
              <div className="rounded-[26px] border border-ink/10 bg-parchment/70 p-3 shadow-inner">
                <label
                  htmlFor="chat-input"
                  className="mb-2 block text-xs uppercase tracking-[0.24em] text-slate"
                >
                  Send a message
                </label>
                <div className="flex flex-col gap-3 md:flex-row">
                  <textarea
                    id="chat-input"
                    value={input}
                    onChange={(event) => setInput(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter" && !event.shiftKey) {
                        event.preventDefault();
                        void sendMessage();
                      }
                    }}
                    rows={3}
                    placeholder="Press Enter to stream a reply. Use Shift + Enter for a new line."
                    className="min-h-[112px] flex-1 resize-none rounded-[22px] border border-ink/10 bg-white px-4 py-3 text-sm text-ink outline-none transition placeholder:text-slate/70 focus:border-ember"
                  />
                  <button
                    type="button"
                    onClick={sendMessage}
                    disabled={sending}
                    className="inline-flex min-w-[156px] items-center justify-center gap-2 rounded-[22px] bg-ember px-5 py-4 text-sm font-semibold text-white transition hover:bg-ember/90 disabled:cursor-not-allowed disabled:opacity-60"
                  >
                    <Send className="h-4 w-4" />
                    {sending ? "Streaming..." : "Send"}
                  </button>
                </div>
              </div>
            </div>
          </main>

          <aside className="order-3 flex min-h-[420px] flex-col gap-4">
            <Panel
              icon={Wrench}
              title="Skill Surface"
              subtitle="Inspect available multi-turn skills and the current runtime footprint."
            >
              <div className="space-y-3">
                {skills.length === 0 ? (
                  <EmptyState
                    title="No skills"
                    description="There are no skill records available to show."
                  />
                ) : (
                  skills.map((skill) => (
                    <div
                      key={skill.name}
                      className="rounded-2xl border border-ink/10 bg-white/70 p-4"
                    >
                      <div className="flex items-center justify-between gap-3">
                        <div className="font-semibold">{skill.name}</div>
                        <span className="rounded-full bg-ink px-2.5 py-1 text-[10px] uppercase tracking-[0.22em] text-parchment">
                          {skill.dynamic ? "dynamic" : "built-in"}
                        </span>
                      </div>
                      <div className="mt-2 text-sm leading-6 text-slate">{skill.description}</div>
                    </div>
                  ))
                )}
              </div>
            </Panel>

            <Panel
              icon={KeyRound}
              title="Access and Health"
              subtitle="Manage the API key and keep a quick pulse on the running service."
            >
              <div className="space-y-4">
                <div className="rounded-2xl border border-ink/10 bg-white/70 p-4">
                  <div className="text-xs uppercase tracking-[0.24em] text-slate">API Key</div>
                  <div className="mt-2 text-sm leading-6 text-slate">
                    {apiKey
                      ? "Stored locally in this browser and sent as a Bearer token."
                      : "Not configured. The app will prompt for it if the server returns 401."}
                  </div>
                  <button
                    type="button"
                    onClick={() => setShowAuth(true)}
                    className="mt-4 inline-flex items-center gap-2 rounded-full border border-ink/10 px-4 py-2 text-sm font-semibold transition hover:border-ember hover:text-ember"
                  >
                    <KeyRound className="h-4 w-4" />
                    {apiKey ? "Edit Key" : "Add Key"}
                  </button>
                </div>

                <div className="rounded-2xl border border-ink/10 bg-white/70 p-4">
                  <div className="text-xs uppercase tracking-[0.24em] text-slate">System Status</div>
                  <dl className="mt-3 space-y-3 text-sm">
                    <StatusRow
                      label="Health"
                      value={health ? "online" : loadingPanel ? "loading" : "offline"}
                    />
                    <StatusRow label="Embedding" value={health?.embedder || "--"} />
                    <StatusRow label="LLM" value={health?.llm || "--"} />
                  </dl>
                </div>

                {errorMessage ? (
                  <div className="rounded-2xl border border-ember/20 bg-ember/10 p-4 text-sm text-ember">
                    {errorMessage}
                  </div>
                ) : null}
              </div>
            </Panel>
          </aside>
        </div>
      </div>

      {showAuth ? (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-ink/45 px-4 backdrop-blur-sm">
          <div className="w-full max-w-md rounded-[30px] border border-ink/10 bg-parchment p-6 shadow-panel">
            <div className="flex items-start gap-3">
              <div className="rounded-2xl bg-ink p-3 text-parchment">
                <KeyRound className="h-5 w-5" />
              </div>
              <div>
                <h3 className="font-display text-2xl font-semibold">Configure credentials</h3>
                <p className="mt-2 text-sm leading-6 text-slate">
                  The value is stored in the browser and attached as `Authorization: Bearer ...` on future requests.
                </p>
              </div>
            </div>

            <label
              className="mt-5 block text-xs uppercase tracking-[0.24em] text-slate"
              htmlFor="api-key-input"
            >
              API Key
            </label>
            <input
              id="api-key-input"
              type="password"
              value={draftKey}
              onChange={(event) => setDraftKey(event.target.value)}
              className="mt-2 w-full rounded-2xl border border-ink/10 bg-white px-4 py-3 text-sm outline-none transition focus:border-ember"
              placeholder="Enter the API key configured on the server"
            />

            <div className="mt-6 flex flex-col-reverse gap-3 sm:flex-row sm:justify-end">
              <button
                type="button"
                onClick={() => setShowAuth(false)}
                className="rounded-full border border-ink/10 px-5 py-2.5 text-sm font-semibold text-slate transition hover:border-ink hover:text-ink"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={saveApiKey}
                className="rounded-full bg-ink px-5 py-2.5 text-sm font-semibold text-parchment transition hover:bg-ink/90"
              >
                Save
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );
}

function parseSSEBlock(block) {
  if (!block.trim()) {
    return null;
  }
  let type = "message";
  const data = [];
  for (const rawLine of block.split(/\r?\n/)) {
    if (rawLine.startsWith("event:")) {
      type = rawLine.slice(6).trim() || "message";
    }
    if (rawLine.startsWith("data:")) {
      data.push(rawLine.slice(5).trimStart());
    }
  }
  return { type, data: data.join("\n") };
}

function Panel({ icon: Icon, title, subtitle, children }) {
  return (
    <section className="rounded-[28px] border border-ink/10 bg-white/80 p-5 shadow-panel backdrop-blur">
      <div className="mb-4 flex items-start gap-3">
        <div className="rounded-2xl bg-ink p-3 text-parchment">
          <Icon className="h-5 w-5" />
        </div>
        <div>
          <h2 className="font-display text-xl font-semibold">{title}</h2>
          <p className="mt-1 text-sm leading-6 text-slate">{subtitle}</p>
        </div>
      </div>
      {children}
    </section>
  );
}

function EmptyState({ title, description }) {
  return (
    <div className="rounded-2xl border border-dashed border-ink/15 bg-ink/5 p-4">
      <div className="font-semibold">{title}</div>
      <div className="mt-1 text-sm leading-6 text-slate">{description}</div>
    </div>
  );
}

function Chip({ icon: Icon, label }) {
  return (
    <div className="inline-flex items-center gap-2 rounded-full border border-ink/10 bg-parchment/80 px-3 py-2 text-xs font-semibold uppercase tracking-[0.2em] text-slate">
      <Icon className="h-3.5 w-3.5" />
      {label}
    </div>
  );
}

function StatusRow({ label, value }) {
  return (
    <div className="flex items-center justify-between gap-4 border-b border-ink/8 pb-3 last:border-b-0 last:pb-0">
      <dt className="text-slate">{label}</dt>
      <dd className="font-semibold uppercase tracking-[0.18em] text-ink">{value}</dd>
    </div>
  );
}
