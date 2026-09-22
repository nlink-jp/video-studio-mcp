# ADR-0009: パスの判定は nlink-jp/pathguard に任せる — 写しを持たない

- Status: Accepted
- Date: 2026-09-22

## Context

ADR-0008 以来、`work_dir` の検証は `internal/workdir` にあった。voice-scribe（組織 ADR-021 の参照実装）
からの写しで、同じ写しがほかの 7 サーバーにもあった。どの写しも場所を**名前で**比べていた。APFS は既定で
大文字小文字を区別しないので、`~/.SSH`、`/USR/local` のような綴りが同じ場所を指しながら検査を通った。
ホームディレクトリが分からないときは、資格情報の位置の検査がすべてを通した。

組織はこの判定を 1 つのモジュールにまとめた（`nlink-jp/pathguard`、lib-series）。場所をファイルの実体と、
ディスクと同じやり方で同一視した名前の両方で比べ、まだ存在しない場所もその親の実体で捕まえる。一覧は
gem-agent・lagent と同じものを 1 つ持つ。

## Decision

- `github.com/nlink-jp/pathguard` v0.1.0 を依存に加える。この org の外のコードは入らない。
- `internal/workdir` は**薄いアダプタ**にする。持つのは次だけ:
  - リクエストの `_meta` を文脈から取り出して `pathguard/workdir` の `Resolve` に渡すこと、
  - その `*workdir.Error` を `toolerr` の同じ code・message・details に移すこと、
  - `NewResolver(serverDirs...)` —— このサーバー自身のディレクトリ（今は設定ディレクトリ
    `~/.config/video-studio-mcp` だけ）を守る場所（`pathguard.ServerDir`）として渡し、`work_dir_required`
    の 1 文を `RequiredHint` で添える。空のパスは何も守らないのではなく、すべての呼び出しを拒ませる
    （設定ディレクトリが決まらないのはホームが分からないときで、そのときは pathguard もすべてを拒む）。
- `Sensitive` は持たない。このサーバーのファイル引数（`manifest_path` と、マニフェストの各ページの画像・音声）はすべて
  ワークスペース相対で、`os.Root` が封じ込める。`work_dir` の外のパスを読むことが無い。
- 呼び出し箇所（`Resolve`・`Validate`）は変えない。変わるのは組み立ての 1 行（`cmd/tools_wiring.go` の
  `workDirResolver`）と、ゼロ値で組み立てていたテストだけである。
- 判定そのもののテストは pathguard にある。ここに残すのはアダプタのテスト（`_meta` の取り出し、エラーの写し、
  守る場所、ゼロ値が拒むこと）と、既存の契約テスト・配線のテストである。

## Consequences

`work_dir` の検査が変わる（CHANGELOG に書く）:

- **新たに拒む**: ランタイムと同じ一覧のうち、自分のホームにある本物の場所（`~/.kube`、`~/.config/gh`、
  `~/.azure`、`~/.terraform.d`、`~/.gemini`、`~/.config/mcp-bridge`、`~/.netrc`、`~/.npmrc`、`~/.pypirc`、
  `~/.git-credentials`、`~/.vault-token`、`~/.docker/config.json`、`~/.claude.json`、`~/.bash_history`、
  `~/.zsh_history`）。床のどの場所についても、大文字小文字の違い・リンク・ファームリンクなど、あらゆる綴り。
  それらのディレクトリの直下にあるリンクの指す先。`$HOME` がアカウントのホームと違うときは、両方を守る。
  Linux の `/etc`。
- **ホームが分からなければ、どの `work_dir` も拒む**。以前は通していた。
- `work_dir_denied` の `details` に `reason` が加わる。
- 1 回の検査は約 2 ms（pathguard の実測）。レンダーの時間に比べて無視できる。

写しを持たないので、判定の修正は pathguard のリリースと、ここでの依存の更新 1 行になる。

## Amendment (2026-09-22): 実際に使うディレクトリも判定する

`work_dir` だけを検査していたので、`work_dir=~/.config` と `workspace_id=gh` でワークスペースが `~/.config/gh` になり、
動画とその素材がそこへ書かれた。ADR-0008 の頃からの穴で、image-forge の独立レビューで見つかった。
`workspace.NewManager(check)` は判定を必須の引数として受け取り、`EnsureUnder` は `<work_dir>/<workspace_id>` を
作る前・使う前に `workdir.Resolver.CheckBeneath`（pathguard v0.2.0）で判定する。配線は `newToolDeps`。判定の無い
Manager はすべてのワークスペースを拒む。pathguard v0.2.0 は NUL バイトを含むパスも拒む。

## Amendment (2026-09-22, v0.6.1): ワークスペースが読むファイルはすべて読む前に判定する

`manifest_path` と、マニフェストが名指す画像・音声はワークスペース相対で `os.Root` 越しに扱うが、床には掛けて
いなかった。ワークスペースは `CheckBeneath` を通っても床の場所を含み得る（`.env`、このサーバーの設定ディレクトリ、
`~/.ssh` 内のリンクが同期フォルダを指すならその行き先）。そうしたファイルはマニフェストとして読まれて先頭が解析エラーに
出（`invalid character 'S'`）、ページとして名指せば受け付けられて ffmpeg に渡された。答えも、無いときの「not found」
「manifest_incomplete」と違った。slack-mcp-extender と chrome-pilot-mcp の独立レビューで見つかった「存在で答えが
変わる」型を、HOME を一時ディレクトリにしたテストで実測して見つけた（15 組中 9 組。仕掛けたリンクで外へ出る 6 組は
`os.Root` が存在に関係なく拒んでいた）。

- ワークスペースは、読む前・探す前にすべての読み取り（`ReadFile`・`Stat`・`VerifyRegular`）を判定する
  （`Workspace.Judge`、internal/workspace/manager.go）—— pathguard の Local 方針とこのサーバー自身のディレクトリ。
  `workspace.NewManager(check, floor)` は床を必須の引数として受け取り（`workdir.Resolver.LocalPath`。無い Manager は
  すべてのワークスペースを拒む）、床の無い Workspace はすべての読み取りを拒む。master ツールは呼び出しの時点で各ページにも
  `Judge` を掛けるので、非同期のレンダリングもジョブができる前に拒む。pathguard がパス上のリンクを自分で辿り、拒否は
  渡されたとおりのパスだけを名指す。マニフェストの拒否は `invalid_manifest` に包まず `path_not_allowed` のまま返す。
  ワークスペースを共有する voice-studio-mcp も同じ作りにした。
- ツール側で判定した最初の版の独立レビューが、ffmpeg の境界で判定を迂回する道を 2 つ見つけた（どちらもこの変更より前から）。
  ページはレンダリング前に 1 度だけ検証され、その後 ffmpeg が 1 枚ずつ開いていたので、レンダリング中（非同期なら数分）に
  リンクへすり替えたページが渡された。今は各画像・音声を、ffprobe・ffmpeg が開く直前に判定込みでもう一度検証する。
  また ffmpeg は画像パスの `%d` を連番として読むので、`p%d.png` という通常ファイルはすべての検査を通り、ffmpeg は
  判定されていない `p0.png`・`p1.png`… を開き得た。パスに `%` を含むページ —— 名前でも、`%` を含む `work_dir` を通した
  ワークスペース自身のパスでも —— は拒む（`master.CheckNames`。呼び出しの時点と `Build` の中で）。ffmpeg がグロブを
  使うのはエスケープされていない `%` の後だけなので、角括弧・波括弧などほかのグロブ文字は普通の文字のまま。
- 普通の入力でもエラーの順序が変わるものがある: 床が拒むページ、字面でワークスペースから出るページ、パスに `%` を含む
  ページは、ジョブを作る前、`ffmpeg_not_found` より前に、呼び出しの時点で答える。リンクであるページは引き続き
  `Build` が見つける。
- `TestExistenceIsNotRevealed` は、同じパスをファイルがある状態と消した状態で、マニフェスト・画像・音声・2 ページ目の画像・
  非同期の画像として `master` を呼び、答え全体を比べ、ファイルの中身が一切出ないことを確かめる。
  `TestEveryReadIsJudgedBeforeItLooks`（internal/workspace）が各読み取りと床の無い Manager・Workspace を、
  `TestAPageNameThatIsAPatternIsRefused`・`TestAWorkspacePathThatIsAPatternIsRefused`・
  `TestAPageSwappedDuringTheRenderIsNotHandedToFFmpeg`（画像と音声）・`TestAnAudioSwappedDuringTheProbesIsNotHandedToFFprobe`
  （internal/master）が ffmpeg の境界を、`TestTheServersWorkspacesJudgeEveryRead`（cmd）がサーバー自身の配線を固定する。
  16 の変異（床を外す・各読み取りの判定を外す・床の無い Manager を受け入れる・床をワークスペースへ渡さない・配線の床を空に
  する・呼び出し時のページの判定を外す・ffmpeg / ffprobe 直前の再検証を外すか音声を外す・パターン名を許す・音声や
  ワークスペースのパスを検査しない・呼び出し時に名前を検査しない・マニフェストの拒否を包む）はすべてアサーションで落ちた。
- pathguard 側の既知の限界（次のリリースに向けて記録）:
  - `work_dir` は pathguard/workdir が組織 ADR-022 §4 の順序（not found が denied より先）で検証するので、資格情報の
    ディレクトリを指す `work_dir` は、存在するかどうかで答えが変わる。
  - 非 ASCII 名のリンク先を別の Unicode 正規化で綴ると、同一性で拒むのはそれが存在するときだけになる（pathguard は
    正規化しない）。別の場所に作ったハードリンクを拒むのは、床の場所そのものであるファイル（`~/.netrc`、
    `~/.docker/config.json` など）へのものだけで、それも存在するときだけ。資格情報ディレクトリの中のファイル
    （`~/.ssh/id_rsa`）や `.env` へのハードリンクは拒まない —— ディレクトリはそれ自身の同一性で比べ、中のファイルでは比べない。
- 判定と開くことは 2 段。各ページは ffprobe・ffmpeg が開く直前にもう一度検証するので、レンダリング中（非同期なら
  数分）にリンクへすり替えたページは拒む。
- ここでは閉じていないもの —— ffmpeg はインタプリタで、これらはその入力引数を変え、本物の ffmpeg で測る必要がある
  （メディア系サーバー横断の後続作業として記録）: ffmpeg は入力の形式を中身から選ぶので、連結リストやプレイリストを
  書いたページのファイルは、誰も判定していないファイルを開かせうる。ワークスペースのディレクトリ自体をレンダリング中に
  リンクへすり替えられる（実パスの検査はワークスペースを作るときの 1 回だけ。床の場所は pathguard がそれでも拒むので、
  破れるのは封じ込めで床ではない）。サーバーが ffmpeg のために書くリスト（連結・章・字幕）は起動直前に検証し直さない。

## References

- 組織 ADR-021（ファイル渡し MCP サーバーの work dir 契約）
- ADR-0008（work dir 契約）: 検証の閉じた一覧 —— その実装をここで置き換える
- nlink-jp/pathguard の RFP（`docs/ja/pathguard-rfp.ja.md`）
