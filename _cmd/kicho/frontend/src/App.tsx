import { useCallback, useEffect, useState } from "react";
import { Clipboard } from "@wailsio/runtime";
import { KifuService, ServerService } from "../bindings/kicho-app";
import type { GameDetail, GameSummary, ServerStatus } from "../bindings/kicho-app/models";
import "./app.css";

type Tab = "fetch" | "import" | "library" | "server";

/**
 * 取得タブのカード1枚。取得するたびに増える。
 *
 * 複数対局を並行して追える（第1局を取ったまま第2局を取れる）ようにするため、
 * 取得結果は1件だけ持つのではなくカードの配列として持つ。
 */
type FetchCard = {
  /**
   * カードの識別子。取得元の棋譜 ID をそのまま使う。
   * 対局中の棋譜を取り直したときにカードが増えず、同じカードが最新化される。
   */
  key: string;
  game: GameDetail;
  /** このカードを保存したもの。保存後に棋譜 URL をコピーできるよう残す。 */
  saved: GameSummary | null;
  notice: string;
  error: string;
};

/**
 * 取得タブの状態。タブを切り替えると中身がアンマウントされるため、
 * 入力した URL と取得結果が消えないよう App 側で保持する。
 *
 * ディスクへの永続化はしない（アプリを閉じればクリアされる）。
 */
type FetchState = {
  input: string;
  /** 新しく取得したものが先頭。 */
  cards: FetchCard[];
  /** 取得そのものの失敗（カードにならないのでここに出す）。 */
  error: string;
};

const emptyFetchState: FetchState = {
  input: "",
  cards: [],
  error: "",
};

/** 登録タブの入力方法。 */
type ImportMode = "url" | "paste";

/** 登録タブの状態。取得タブと同様、タブを跨いでも消えないよう App 側で保持する。 */
type ImportState = {
  mode: ImportMode;
  url: string;
  text: string;
  preview: GameDetail | null;
  saved: GameSummary | null;
  error: string;
  notice: string;
};

const emptyImportState: ImportState = {
  mode: "url",
  url: "",
  text: "",
  preview: null,
  saved: null,
  error: "",
  notice: "",
};

/** RFC3339 の開始日時を表示用に整える(未設定なら "-")。 */
function formatDate(rfc3339: string): string {
  if (!rfc3339) return "-";
  const d = new Date(rfc3339);
  if (Number.isNaN(d.getTime())) return rfc3339;
  return d.toLocaleDateString("ja-JP", { year: "numeric", month: "2-digit", day: "2-digit" });
}

/** Go 側から返るエラーを表示用の文字列にする。 */
function errorMessage(e: unknown): string {
  if (e instanceof Error) return e.message;
  return String(e);
}

/**
 * URL をクリップボードへコピーするボタン。
 *
 * URL の組み立ては Go 側（httpapi のパス組み立て + サーバ設定）に任せる。
 * `urls` は先頭が localhost 版で、LAN 公開時は LAN アドレス版も続く。
 */
function CopyURLButton({
  label,
  title,
  load,
}: {
  label: string;
  title: string;
  load: () => Promise<string[] | null>;
}) {
  const [state, setState] = useState<"idle" | "done" | "error">("idle");
  const [message, setMessage] = useState("");

  const copy = async () => {
    try {
      const urls = await load();
      if (!urls || urls.length === 0) throw new Error("URL を取得できませんでした");
      // LAN 公開時も含め、コピーするのは先頭（localhost 版）。
      await Clipboard.SetText(urls[0]);
      setMessage(urls[0]);
      setState("done");
    } catch (e) {
      setMessage(errorMessage(e));
      setState("error");
    }
    setTimeout(() => setState("idle"), 2500);
  };

  return (
    <span className="copy">
      <button onClick={copy} title={title}>
        {state === "done" ? "コピーしました" : label}
      </button>
      {state === "done" && <span className="copied-url">{message}</span>}
      {state === "error" && <span className="copy-error">{message}</span>}
    </span>
  );
}

/**
 * 手数の表示。終局していれば「(終局)」を添える。
 * 対局中の棋譜は随時更新されるため、保存済みでも終局済みとは限らない。
 */
function Moves({ moves, finished, endMark }: { moves: number; finished: boolean; endMark: string }) {
  return (
    <>
      {moves}
      {finished && (
        <span className="ended" title={endMark}>
          （終局）
        </span>
      )}
    </>
  );
}

export default function App() {
  const [tab, setTab] = useState<Tab>("fetch");
  // 取得タブの入力・取得結果はタブを跨いでも保持する。
  const [fetchState, setFetchState] = useState<FetchState>(emptyFetchState);
  const [importState, setImportState] = useState<ImportState>(emptyImportState);
  // 保存したら棋譜一覧を読み直させる。
  const [libraryRevision, setLibraryRevision] = useState(0);

  return (
    <div className="app">
      <header className="header">
        <h1>
          kicho <span className="header-sub">棋帳</span>
        </h1>
        <nav className="tabs">
          <button className={tab === "fetch" ? "active" : ""} onClick={() => setTab("fetch")}>
            取得
          </button>
          <button className={tab === "import" ? "active" : ""} onClick={() => setTab("import")}>
            登録
          </button>
          <button className={tab === "library" ? "active" : ""} onClick={() => setTab("library")}>
            棋譜一覧
          </button>
          <button className={tab === "server" ? "active" : ""} onClick={() => setTab("server")}>
            サーバ
          </button>
        </nav>
      </header>

      <main className="main">
        {tab === "fetch" && (
          <FetchTab
            state={fetchState}
            setState={setFetchState}
            onSaved={() => setLibraryRevision((n) => n + 1)}
          />
        )}
        {tab === "import" && (
          <ImportTab
            state={importState}
            setState={setImportState}
            onSaved={() => setLibraryRevision((n) => n + 1)}
          />
        )}
        {tab === "library" && <LibraryTab revision={libraryRevision} />}
        {tab === "server" && <ServerTab />}
      </main>
    </div>
  );
}

/**
 * 取得タブ: URL または棋譜 ID から取得してカードを並べ、カードごとに保存する。
 *
 * 複数の対局を同時に追えるよう、取得するたびにカードを追加する。
 * すでに取得済みの棋譜をもう一度取ったときは、カードを増やさずその場で最新化する
 * （対局中は同じ棋譜を繰り返し取り直すため）。
 */
function FetchTab({
  state,
  setState,
  onSaved,
}: {
  state: FetchState;
  setState: React.Dispatch<React.SetStateAction<FetchState>>;
  onSaved: () => void;
}) {
  // 通信中フラグは一時的なものなのでタブ内に持つ。
  const [busy, setBusy] = useState(false);
  // 保存中のカード。保存はカード単位なのでどれを処理中か持つ。
  const [savingKey, setSavingKey] = useState("");

  const { input, cards, error } = state;
  const patch = (p: Partial<FetchState>) => setState((s) => ({ ...s, ...p }));
  const patchCard = (key: string, p: Partial<FetchCard>) =>
    setState((s) => ({
      ...s,
      cards: s.cards.map((c) => (c.key === key ? { ...c, ...p } : c)),
    }));

  const handleFetch = async () => {
    setBusy(true);
    patch({ error: "" });
    try {
      const d = await KifuService.Fetch(input);
      const key = d.sourceId || d.sourceUrl || input.trim();
      setState((s) => {
        const i = s.cards.findIndex((c) => c.key === key);
        // 取り直しなら、位置と保存済みの情報を保ったまま中身だけ差し替える。
        if (i >= 0) {
          const cards = [...s.cards];
          cards[i] = { ...cards[i], game: d, notice: "取得し直しました", error: "" };
          return { ...s, cards };
        }
        const card: FetchCard = { key, game: d, saved: null, notice: "", error: "" };
        return { ...s, cards: [card, ...s.cards] };
      });
    } catch (e) {
      patch({ error: errorMessage(e) });
    } finally {
      setBusy(false);
    }
  };

  // 保存は取得し直さず、いま表示している内容をそのまま書き込む。
  // 入力とカードは残す（保存後に URL をコピーしたり取り直したりできるように）。
  const handleSave = async (card: FetchCard) => {
    setSavingKey(card.key);
    patchCard(card.key, { error: "", notice: "" });
    try {
      const rec = await KifuService.Save(card.game);
      patchCard(card.key, { saved: rec, notice: `保存しました: ${rec.event || rec.sourceId}` });
      onSaved();
    } catch (e) {
      patchCard(card.key, { error: errorMessage(e) });
    } finally {
      setSavingKey("");
    }
  };

  const removeCard = (key: string) =>
    setState((s) => ({ ...s, cards: s.cards.filter((c) => c.key !== key) }));

  return (
    <section>
      <h2>棋譜を取得</h2>
      <p className="hint">
        読売(竜王戦)の対局ページ URL、棋譜ビューアの URL、棋譜 ID のいずれかを入力してください。
        取得した内容を確認してから保存します。取得するたびにカードが増えるので、
        複数の対局を並べて追えます（同じ棋譜を取り直したときはそのカードが最新化されます）。
      </p>

      <div className="row">
        <input
          className="grow"
          type="text"
          value={input}
          placeholder="https://www.yomiuri.co.jp/igoshougi/ryuoh/kifu/..."
          onChange={(e) => patch({ input: e.target.value })}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !busy && input.trim()) handleFetch();
          }}
        />
        <button className="primary" onClick={handleFetch} disabled={busy || !input.trim()}>
          取得
        </button>
        <button
          onClick={() => setState(emptyFetchState)}
          disabled={busy || (!input && cards.length === 0)}
          title="入力と取得したカードをすべてクリアする"
        >
          クリア
        </button>
      </div>

      {busy && <p className="notice">通信中…</p>}
      {error && <p className="error">{error}</p>}

      {cards.map((card) => (
        <FetchCardView
          key={card.key}
          card={card}
          busy={savingKey === card.key}
          onSave={() => handleSave(card)}
          onRemove={() => removeCard(card.key)}
        />
      ))}
    </section>
  );
}

/** 取得タブのカード1枚。プレビューと、そのカードに対する保存・削除。 */
function FetchCardView({
  card,
  busy,
  onSave,
  onRemove,
}: {
  card: FetchCard;
  busy: boolean;
  onSave: () => void;
  onRemove: () => void;
}) {
  const { game, saved, notice, error } = card;

  return (
    <div className="preview">
      <div className="row space-between">
        <h3>{game.event || "(棋戦名なし)"}</h3>
        <button
          className="danger"
          onClick={onRemove}
          disabled={busy}
          title="このカードを閉じる（保存済みの棋譜は消えない）"
        >
          削除
        </button>
      </div>

      <GamePreview game={game} showTitle={false} />

      {!game.finished && (
        <p className="hint">
          まだ終局していません。対局中の棋譜は随時更新されるため、
          保存しても後で取り直す必要があります（同じ棋譜なら保存し直しても増えません）。
          対局中はこのまま「取得 URL」を外部ツールに渡すと、開くたびに最新が取れます。
        </p>
      )}

      {error && <p className="error">{error}</p>}
      {notice && <p className="notice">{notice}</p>}

      <div className="row">
        <button className="primary" onClick={onSave} disabled={busy}>
          {saved ? "保存し直す" : "この内容を保存"}
        </button>
        {saved && (
          <CopyURLButton
            label="棋譜 URL をコピー"
            title="保存済み棋譜の URL（サイトへは取りに行かない）"
            load={() => ServerService.KifuURLs(saved.id)}
          />
        )}
        <CopyURLButton
          label="取得 URL をコピー"
          title="開くたびにサイトから取り直す URL（対局中向け）"
          load={() => ServerService.SourceURLs(game.sourceId)}
        />
      </div>
    </div>
  );
}

/**
 * 取得・登録の共通プレビュー（読み取れた対局情報と KIF 本文）。
 * 取得タブのカードは見出しの行に削除ボタンを並べるため、棋戦名を自分で出す（showTitle=false）。
 */
function GamePreview({ game, showTitle = true }: { game: GameDetail; showTitle?: boolean }) {
  return (
    <>
      {showTitle && <h3>{game.event || "(棋戦名なし)"}</h3>}
      <dl className="meta">
        <dt>先手</dt>
        <dd>{game.black || "-"}</dd>
        <dt>後手</dt>
        <dd>{game.white || "-"}</dd>
        <dt>手合割</dt>
        <dd>{game.handicap || "-"}</dd>
        <dt>開始日時</dt>
        <dd>{formatDate(game.startedAt)}</dd>
        <dt>場所</dt>
        <dd>{game.place || "-"}</dd>
        <dt>手数</dt>
        <dd>
          <Moves moves={game.moves} finished={game.finished} endMark={game.endMark} />
        </dd>
      </dl>
      <details>
        <summary>KIF を表示</summary>
        <pre className="kif">{game.kif}</pre>
      </details>
    </>
  );
}

/**
 * 登録タブ: URL から取得、または KIF を貼り付けて登録する。
 *
 * 読売のスクレイピング（取得タブ）と違い、取得元での一意な ID が無いため
 * 登録のたびに新しい棋譜として保存される。
 */
function ImportTab({
  state,
  setState,
  onSaved,
}: {
  state: ImportState;
  setState: React.Dispatch<React.SetStateAction<ImportState>>;
  onSaved: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const { mode, url, text, preview, saved, error, notice } = state;
  const patch = (p: Partial<ImportState>) => setState((s) => ({ ...s, ...p }));

  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    patch({ error: "", notice: "" });
    try {
      await fn();
    } catch (e) {
      patch({ error: errorMessage(e) });
    } finally {
      setBusy(false);
    }
  };

  const source = mode === "url" ? url : text;
  const canRun = source.trim().length > 0;

  const handlePreview = () =>
    run(async () => {
      const d =
        mode === "url" ? await KifuService.PreviewURL(url) : await KifuService.PreviewKIF(text);
      patch({ preview: d, saved: null });
    });

  const handleImport = () =>
    run(async () => {
      const rec =
        mode === "url" ? await KifuService.ImportURL(url) : await KifuService.ImportKIF(text);
      patch({ saved: rec, notice: `登録しました: ${rec.event || "(棋戦名なし)"}` });
      onSaved();
    });

  return (
    <section>
      <h2>棋譜を登録</h2>
      <p className="hint">
        KIF 形式の棋譜を登録します。棋戦名・対局者・日付・手数は KIF のヘッダから読み取ります。
      </p>

      <div className="row">
        <label className="checkbox">
          <input
            type="radio"
            checked={mode === "url"}
            onChange={() => patch({ mode: "url", preview: null, saved: null })}
          />
          URL から取得
        </label>
        <label className="checkbox">
          <input
            type="radio"
            checked={mode === "paste"}
            onChange={() => patch({ mode: "paste", preview: null, saved: null })}
          />
          貼り付け
        </label>
      </div>

      {mode === "url" ? (
        <div className="row">
          <input
            className="grow"
            type="text"
            value={url}
            placeholder="https://example.com/kifu/20241019.kif"
            onChange={(e) => patch({ url: e.target.value })}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !busy && canRun) handlePreview();
            }}
          />
        </div>
      ) : (
        <textarea
          className="kif-input"
          value={text}
          placeholder={"開始日時：2024/10/19 09:00:00\n先手：…\n後手：…\n手数----指手---------消費時間--\n   1 ７六歩(77)"}
          onChange={(e) => patch({ text: e.target.value })}
        />
      )}

      <div className="row">
        <button onClick={handlePreview} disabled={busy || !canRun}>
          内容を確認
        </button>
        <button className="primary" onClick={handleImport} disabled={busy || !canRun}>
          登録
        </button>
        <button
          onClick={() => setState(emptyImportState)}
          disabled={busy || (!canRun && !preview)}
        >
          クリア
        </button>
      </div>

      {busy && <p className="notice">処理中…</p>}
      {error && <p className="error">{error}</p>}
      {notice && <p className="notice">{notice}</p>}

      {mode === "url" && (
        <p className="hint">
          URL の中身をそのまま KIF として読みます（スクレイピングはしません）。
          Shift_JIS の .kif も自動で判別します。他の kicho の
          <code> /kifu/&#123;id&#125; </code>
          も取り込めます。
        </p>
      )}

      {preview && (
        <div className="preview">
          <GamePreview game={preview} />
          <p className="hint">
            登録のたびに新しい棋譜として保存されます（同じものを2回登録すると2件になります）。
          </p>
          <div className="row">
            <button className="primary" onClick={handleImport} disabled={busy}>
              この内容で登録
            </button>
            {saved && (
              <CopyURLButton
                label="棋譜 URL をコピー"
                title="登録した棋譜の URL"
                load={() => ServerService.KifuURLs(saved.id)}
              />
            )}
          </div>
        </div>
      )}

      {!preview && saved && (
        <div className="preview">
          <h3>{saved.event || "(棋戦名なし)"}</h3>
          <p className="hint">
            手数 <Moves moves={saved.moves} finished={saved.finished} endMark={saved.endMark} />
          </p>
          <CopyURLButton
            label="棋譜 URL をコピー"
            title="登録した棋譜の URL"
            load={() => ServerService.KifuURLs(saved.id)}
          />
        </div>
      )}
    </section>
  );
}

/**
 * 棋譜一覧タブ: 保存済みの棋譜を一覧・表示・削除する。
 * revision が変わると読み直す（取得タブで保存したときに反映するため）。
 */
function LibraryTab({ revision }: { revision: number }) {
  const [games, setGames] = useState<GameSummary[]>([]);
  const [total, setTotal] = useState(0);
  const [selected, setSelected] = useState<GameDetail | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  // 検索条件
  const [text, setText] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [finishedOnly, setFinishedOnly] = useState(false);

  const search = useCallback(
    async (q: { text: string; from: string; to: string; finishedOnly: boolean }) => {
      setLoading(true);
      setError("");
      try {
        const [list, n] = await Promise.all([
          KifuService.Search({ ...q, limit: 0 }),
          KifuService.Count(),
        ]);
        setGames(list ?? []);
        setTotal(n);
      } catch (e) {
        setError(errorMessage(e));
      } finally {
        setLoading(false);
      }
    },
    [],
  );

  const reload = useCallback(
    () => search({ text, from, to, finishedOnly }),
    [search, text, from, to, finishedOnly],
  );

  // 初回とタブ復帰時、保存後は現在の条件で読み直す。
  // 入力のたびには検索しない（Enter か「検索」ボタンで実行する）。
  useEffect(() => {
    search({ text, from, to, finishedOnly });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [search, revision]);

  const clearConditions = () => {
    setText("");
    setFrom("");
    setTo("");
    setFinishedOnly(false);
    search({ text: "", from: "", to: "", finishedOnly: false });
  };

  const hasConditions = text !== "" || from !== "" || to !== "" || finishedOnly;
  const shortText = text.length > 0 && text.length < 3;

  const show = async (id: string) => {
    setError("");
    try {
      setSelected(await KifuService.Get(id));
    } catch (e) {
      setError(errorMessage(e));
    }
  };

  const remove = async (id: string) => {
    setError("");
    try {
      await KifuService.Delete(id);
      if (selected?.id === id) setSelected(null);
      await reload();
    } catch (e) {
      setError(errorMessage(e));
    }
  };

  return (
    <section>
      <div className="row space-between">
        <h2>
          棋譜一覧（{games.length}
          {hasConditions && total > 0 && ` / ${total}`}）
        </h2>
        <button onClick={reload} disabled={loading}>
          再読み込み
        </button>
      </div>

      <div className="row search">
        <input
          className="grow"
          type="text"
          value={text}
          placeholder="棋戦名・対局者・場所で検索"
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") reload();
          }}
        />
        <label>
          期間
          <input type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
        </label>
        <label>
          〜
          <input type="date" value={to} onChange={(e) => setTo(e.target.value)} />
        </label>
        <label className="checkbox">
          <input
            type="checkbox"
            checked={finishedOnly}
            onChange={(e) => setFinishedOnly(e.target.checked)}
          />
          終局済みのみ
        </label>
        <button className="primary" onClick={reload} disabled={loading}>
          検索
        </button>
        <button onClick={clearConditions} disabled={loading || !hasConditions}>
          条件をクリア
        </button>
      </div>

      {shortText && (
        <p className="hint">
          検索語が 3 文字未満です。索引が使えないため全件を走査します（件数が増えると遅くなります）。
        </p>
      )}

      {error && <p className="error">{error}</p>}
      {loading && <p className="notice">読み込み中…</p>}
      {!loading && games.length === 0 && !hasConditions && (
        <p className="hint">まだ棋譜がありません。「取得」タブから追加してください。</p>
      )}
      {!loading && games.length === 0 && hasConditions && (
        <p className="hint">条件に合う棋譜がありません。</p>
      )}

      {games.length > 0 && (
        <table className="table">
          <thead>
            <tr>
              <th>開始日</th>
              <th>棋戦</th>
              <th>先手</th>
              <th>後手</th>
              <th className="num">手数</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {games.map((g) => (
              <tr key={g.id} className={selected?.id === g.id ? "selected" : ""}>
                <td>{formatDate(g.startedAt)}</td>
                <td>{g.event || "-"}</td>
                <td>{g.black || "-"}</td>
                <td>{g.white || "-"}</td>
                <td className="num">
                  <Moves moves={g.moves} finished={g.finished} endMark={g.endMark} />
                </td>
                <td className="actions">
                  <CopyURLButton
                    label="URL"
                    title="保存済み棋譜の URL をコピー"
                    load={() => ServerService.KifuURLs(g.id)}
                  />
                  <button onClick={() => show(g.id)}>表示</button>
                  <button className="danger" onClick={() => remove(g.id)}>
                    削除
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {selected && (
        <div className="preview">
          <div className="row space-between">
            <h3>{selected.event || "(棋戦名なし)"}</h3>
            <button onClick={() => setSelected(null)}>閉じる</button>
          </div>
          <div className="row">
            <CopyURLButton
              label="棋譜 URL をコピー"
              title="保存済み棋譜の URL（サイトへは取りに行かない）"
              load={() => ServerService.KifuURLs(selected.id)}
            />
            {selected.sourceId && (
              <CopyURLButton
                label="取得 URL をコピー"
                title="開くたびにサイトから取り直す URL（対局中向け）"
                load={() => ServerService.SourceURLs(selected.sourceId)}
              />
            )}
          </div>
          <pre className="kif">{selected.kif}</pre>
        </div>
      )}
    </section>
  );
}

/** サーバタブ: 棋譜配信 HTTP サーバの設定と起動/停止。 */
function ServerTab() {
  const [status, setStatus] = useState<ServerStatus | null>(null);
  const [host, setHost] = useState("127.0.0.1");
  const [port, setPort] = useState(3000);
  const [autoStart, setAutoStart] = useState(true);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const apply = (s: ServerStatus) => {
    setStatus(s);
    setHost(s.host);
    setPort(s.port);
    setAutoStart(s.autoStart);
  };

  const run = useCallback(async (fn: () => Promise<ServerStatus>) => {
    setBusy(true);
    setError("");
    try {
      apply(await fn());
    } catch (e) {
      setError(errorMessage(e));
      // 失敗しても現在状態は取り直す(起動できたか否かを正しく出すため)。
      try {
        setStatus(await ServerService.Status());
      } catch {
        /* 取得できなければ前回の表示のままにする */
      }
    } finally {
      setBusy(false);
    }
  }, []);

  useEffect(() => {
    run(() => ServerService.Status());
  }, [run]);

  const willBePublic = host !== "127.0.0.1" && host !== "localhost" && host !== "::1";

  return (
    <section>
      <h2>棋譜配信サーバ</h2>
      <p className="hint">
        ShogiHome などの外部ツールが URL から棋譜を取得するための HTTP サーバです。
      </p>

      <div className="status">
        <span className={status?.running ? "dot on" : "dot off"} />
        {status?.running ? `稼働中 (${status.addr})` : "停止中"}
      </div>

      {status?.urls && status.urls.length > 0 && (
        <>
          <ul className="urls">
            {status.urls.map((u) => (
              <li key={u}>
                <code>{u}</code>
              </li>
            ))}
          </ul>
          <p className="hint">
            個別の棋譜 URL は「棋譜一覧」タブの各行からコピーできます。
          </p>
        </>
      )}

      <div className="row">
        <label>
          bind
          <select value={host} onChange={(e) => setHost(e.target.value)}>
            <option value="127.0.0.1">127.0.0.1（この PC のみ）</option>
            <option value="0.0.0.0">0.0.0.0（LAN に公開）</option>
          </select>
        </label>
        <label>
          ポート
          <input
            type="number"
            min={1}
            max={65535}
            value={port}
            onChange={(e) => setPort(Number(e.target.value))}
          />
        </label>
        <label className="checkbox">
          <input
            type="checkbox"
            checked={autoStart}
            onChange={(e) => setAutoStart(e.target.checked)}
          />
          起動時に自動で開始
        </label>
      </div>

      {willBePublic && (
        <p className="warning">
          ⚠ 0.0.0.0 で待ち受けると、同じネットワーク上の他の端末から保存済みの棋譜を
          誰でも取得できる状態になります。認証はありません。
        </p>
      )}

      <div className="row">
        <button
          onClick={() => run(() => ServerService.UpdateSettings(host, port, autoStart))}
          disabled={busy}
        >
          設定を保存
        </button>
        <button
          className="primary"
          onClick={() => run(() => ServerService.Start())}
          disabled={busy || status?.running}
        >
          開始
        </button>
        <button
          onClick={() => run(() => ServerService.Stop())}
          disabled={busy || !status?.running}
        >
          停止
        </button>
      </div>

      {error && <p className="error">{error}</p>}
    </section>
  );
}
