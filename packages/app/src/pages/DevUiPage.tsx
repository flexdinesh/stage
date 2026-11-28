// Primitives gallery (/dev/ui): every shared component in its states, for visual checks in both themes and at
// phone width. Unlinked and lazy-loaded; not a product page.
import { useState, type ReactNode } from "react";
import { IconBell, IconClose, IconPlus, IconRefresh, IconSearch } from "../components/icons.tsx";
import { RequestCard, RowRequestControls, RowRequestLine } from "../components/Requests.tsx";
import { FollowUpPanel } from "../components/FollowUp.tsx";
import { ApiError } from "../api/client.ts";
import type { ActionResult, Capabilities, ExecutionConfig, HarnessAction, SessionRequest } from "../api/types.ts";
import { CapabilityList } from "../components/ConnectionCapabilities.tsx";
import { HarnessFields } from "../components/HarnessFields.tsx";
import { SessionToolbarActions } from "../components/SessionToolbarActions.tsx";
import { LaunchMeta } from "../components/LaunchMeta.tsx";
import { launchParts } from "../lib/capabilities.ts";
import { rowRequestAction } from "../lib/requests.ts";
import { replyPrompt, type FollowUpView } from "../lib/followup.ts";
import {
  Alert,
  Button,
  Checkbox,
  ChoiceRow,
  Chip,
  Combobox,
  ConfirmInline,
  CornerBadge,
  CopyButton,
  CommandBox,
  Detail,
  Details,
  DisclosureButton,
  Drawer,
  EmptyState,
  ErrorText,
  ExternalLink,
  Field,
  FieldGroup,
  Highlight,
  Hint,
  IconButton,
  IdTag,
  InlineError,
  Input,
  KeyHints,
  Kbd,
  Marker,
  Menu,
  MenuItem,
  MenuSeparator,
  Meta,
  MetaLine,
  Muted,
  PageHeader,
  Panel,
  PanelHeader,
  PopoverPanel,
  Radio,
  RelTime,
  Row,
  RowButton,
  RowList,
  SectionHeading,
  Segmented,
  Skeleton,
  SkeletonBar,
  SkeletonRows,
  SubHeading,
  TextButton,
  TextLink,
  Textarea,
  needEdgeClass,
  useMenu,
  usePopover,
  type ChipTone,
} from "../components/ui.tsx";

const TONES: readonly ChipTone[] = ["need", "run", "done", "ok", "bad", "idle", "plain"];
const BADGES: readonly (readonly [number, "plain" | "need" | "bad"])[] = [[3, "plain"], [55, "plain"], [120, "plain"], [7, "need"], [2, "bad"]];
const COLORS = ["bg", "surface", "panel", "line", "line-strong", "fg", "muted", "accent", "need", "need-fg", "run", "done", "ok", "bad", "idle"] as const;
const TYPE_ROLES = ["label", "meta-sm", "meta", "detail", "small", "ui", "body", "title", "headline"] as const;

const choice = (id: string, label: string) => ({ id, label, available: true });
const SAMPLE_APPROVAL: SessionRequest = {
  id: "r1",
  kind: "approval",
  title: "Run a command",
  detail: "Create p2.txt and echo made",
  preview: "touch p2.txt && echo made",
  options: [choice("allow", "Allow"), choice("deny", "Deny")],
  textOption: "deny",
  createdAt: "2026-11-25T07:00:00Z",
};
const SAMPLE_ALWAYS: SessionRequest = {
  ...SAMPLE_APPROVAL,
  id: "r2",
  title: "Run a command",
  detail: "Run the migration check",
  preview: Array.from({ length: 12 }, (_, i) => `step ${i + 1}: go test ./internal/db/... -run TestMigration${i + 1}`).join("\n"),
  options: [
    choice("allow", "Allow"),
    choice("always:0", "Always allow `Bash(go test:*)` in this project (.claude/settings.local.json)"),
    choice("always:1", "Allow and accept edits for this session"),
    choice("deny", "Deny"),
  ],
};
const SAMPLE_QUESTION: SessionRequest = {
  id: "r3",
  kind: "question",
  title: "Claude Code asks",
  questions: [
    { id: "0", header: "Color", prompt: "Which color do you prefer?", options: [{ label: "Red", description: "Warm" }, { label: "Blue", description: "Cool" }], custom: true },
    { id: "1", header: "Sizes", prompt: "Which sizes ship first?", options: [{ label: "Small" }, { label: "Medium" }, { label: "Large" }], multi: true },
    { id: "2", header: "Name", prompt: "What should the release be called?", options: [], custom: true },
  ],
  createdAt: "2026-11-25T07:00:00Z",
};
const noop = () => undefined;
// A harness as its capabilities declare it: the Harness group, the capability list and the launch meta line read only this.
const SAMPLE_CAPS: Capabilities = {
  models: [
    { id: "gpt-5", label: "GPT-5", available: true, group: "", permissions: null, options: [{ id: "effort", kind: "choice", label: "Reasoning effort", values: ["low", "medium", "high"], default: "medium" }] },
    { id: "gpt-5-mini", label: "GPT-5 mini", available: true, group: "", permissions: null, options: null },
  ],
  options: [
    { id: "worktree", kind: "boolean", label: "Worktree", values: [true, false], default: false },
    { id: "baseRef", kind: "text", label: "Base branch", values: [], dependsOn: { option: "worktree", value: true } },
  ],
  permissions: [choice("read-only", "Read only"), choice("workspace", "Workspace write"), choice("full", "Full access")],
  modes: null,
  operations: {
    launch: { available: true },
    stop: { available: true },
    adopt: { available: true },
    reply: { available: true },
    opening: { available: false, reason: "Codex does not keep a session's first prompt apart from its history." },
    attention: { available: true },
    images: { available: true },
    respond: { available: false, reason: "Approvals are answered in the Codex terminal." },
    send: { available: false },
    sessionRename: { available: true },
  },
};
const SAMPLE_CONFIG: ExecutionConfig = { model: "gpt-5", options: [{ id: "effort", value: "high" }, { id: "worktree", value: true }, { id: "baseRef", value: "main" }], permission: "workspace", mode: "" };

function HarnessFieldsDemo() {
  const [config, setConfig] = useState(SAMPLE_CONFIG);
  return <HarnessFields title="Harness · Codex" config={config} caps={SAMPLE_CAPS} onChange={setConfig} disabled={false} />;
}
const BUSY_ERROR = new ApiError(409, "Claude Code is working. Send once it finishes.", "session_busy");

/** One composer with its own draft, as task detail holds it. */
function FollowUpDemo({ view, initial = "", ...rest }: { view: FollowUpView; initial?: string; sentAt?: string; error?: unknown }) {
  const [draft, setDraft] = useState(initial);
  return <FollowUpPanel view={view} harness="Claude Code" draft={draft} onDraft={setDraft} onSend={noop} {...rest} />;
}

const SAMPLE_SESSION_ACTIONS: HarnessAction[] = [
  { id: "settle", label: "Settle in T3", target: "session", destructive: false, callers: ["user", "agent"], availability: { available: true },
    confirm: "T3 settles this session. If this connection infers acceptance from harness closure, Stage completes the task." },
  { id: "archive", label: "Archive in T3", target: "session", destructive: false, callers: ["user", "agent"], availability: { available: true },
    confirm: "T3 archives this session. Stage completes the task only when T3 settles it." },
];

const runSampleAction = (id: string): Promise<ActionResult> => new Promise((resolve) => {
  setTimeout(() => resolve({ message: id === "settle" ? "Settled in T3." : "Archived in T3." }), 600);
});

function SessionActionsDemo({ actions }: { actions: HarnessAction[] }) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Button>Open in T3</Button>
      <Button>Stop</Button>
      <SessionToolbarActions actions={actions} run={runSampleAction} />
      <Button>Duplicate</Button>
      <Button>Edit</Button>
      <Button className="ml-auto">Archive</Button>
    </div>
  );
}

function Section({ title, meta, children }: { title: string; meta?: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-2">
      <SectionHeading title={title} meta={meta} />
      {children}
    </section>
  );
}

function Swatch({ name }: { name: (typeof COLORS)[number] }) {
  return (
    <div className="flex items-center gap-2">
      <span className="size-6 shrink-0 border border-line" style={{ background: `var(--${name})` }} />
      <Meta>{name}</Meta>
    </div>
  );
}

function OverlayDemos() {
  const menu = useMenu();
  const popover = usePopover();
  const [drawer, setDrawer] = useState(false);
  return (
    <div className="flex flex-wrap items-start gap-3">
      <Button {...menu.triggerProps}>Menu</Button>
      <Menu menu={menu} label="Demo actions" className="w-52">
        <MenuItem onSelect={() => undefined}>Rename</MenuItem>
        <MenuItem onSelect={() => undefined} leading="task">
          Open with a leading kind
        </MenuItem>
        <MenuSeparator />
        <MenuItem onSelect={() => undefined} reason="Stop the running task first">
          Mark done
        </MenuItem>
        <MenuItem onSelect={() => undefined} danger>
          Delete
        </MenuItem>
      </Menu>
      <div {...popover.wrapProps} className="relative">
        <Button {...popover.triggerProps}>Popover</Button>
        <PopoverPanel popover={popover} label="Demo popover" className="absolute top-full left-0 mt-1 w-72">
          <PanelHeader title="Servers" meta="2" />
          <RowList>
            <li>
              <RowButton selected>
                <Marker tone="ok" />
                <span className="font-mono text-meta">this computer</span>
              </RowButton>
            </li>
            <li>
              <RowButton>
                <Marker tone="idle" />
                <span className="font-mono text-meta">deebox</span>
              </RowButton>
            </li>
          </RowList>
        </PopoverPanel>
      </div>
      <Button onClick={() => setDrawer(true)}>Drawer</Button>
      {drawer ? (
        <Drawer title="Drawer" onClose={() => setDrawer(false)} footer={<Button variant="primary">Save</Button>}>
          <Muted>Drawer body. Esc closes; focus is trapped and restored.</Muted>
        </Drawer>
      ) : null}
    </div>
  );
}

/** A fixed sample time for the gallery's relative-time demos. */
const SAMPLE_AT = "2026-01-01T09:00:00.000Z";

export function DevUiPage() {
  const [seg, setSeg] = useState<"open" | "closed" | "all">("open");
  const [combo, setCombo] = useState("b");
  const [confirming, setConfirming] = useState(true);
  const [folded, setFolded] = useState(false);
  const [checked, setChecked] = useState(true);
  const [radio, setRadio] = useState("a");

  return (
    <div className="flex flex-col gap-8 pb-12">
      <PageHeader title="UI primitives" meta="components/ui · tokens in index.css · DESIGN.md Primitives" />

      <Section title="Tokens" meta="color and type roles">
        <Panel>
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-5">
            {COLORS.map((c) => (
              <Swatch key={c} name={c} />
            ))}
          </div>
        </Panel>
        <Panel flush>
          <RowList>
            {TYPE_ROLES.map((r) => (
              <Row key={r} className="flex items-baseline gap-4">
                <Meta className="w-20 shrink-0">{r}</Meta>
                <span style={{ fontSize: `var(--fs-${r})` }}>The ink workbench</span>
              </Row>
            ))}
          </RowList>
        </Panel>
      </Section>

      <Section title="Buttons">
        <Panel>
          <div className="flex flex-col gap-3">
            <div className="flex flex-wrap items-center gap-2">
              <Button variant="primary">Primary</Button>
              <Button>Secondary</Button>
              <Button variant="ghost">Ghost</Button>
              <Button variant="danger">Delete</Button>
              <Button busy>Busy</Button>
              <Button disabled>Disabled</Button>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <Button small variant="primary">
                Small primary
              </Button>
              <Button small>Small</Button>
              <Button small variant="ghost">
                Small ghost
              </Button>
              <IconButton label="New task" icon={IconPlus} variant="primary" />
              <IconButton label="Search" icon={IconSearch} variant="secondary" />
              <IconButton label="Close" icon={IconClose} />
              <IconButton label="Close" icon={IconClose} small />
              <IconButton label="Refresh" icon={IconRefresh} variant="secondary" busy />
              <TextButton className="text-ui">Text button</TextButton>
            </div>
            <div className="flex flex-wrap items-center gap-3">
              {BADGES.map(([count, tone]) => (
                <span key={`${count}-${tone}`} className="relative inline-flex">
                  <IconButton label={`Notifications, ${count} unread`} icon={IconBell} variant="secondary" />
                  <CornerBadge count={count} tone={tone} />
                </span>
              ))}
            </div>
          </div>
        </Panel>
      </Section>

      <Section title="Chips and markers">
        <Panel>
          <div className="flex flex-col gap-3">
            <div className="flex flex-wrap gap-2">
              {TONES.map((t) => (
                <Chip key={t} tone={t} dot>
                  {t}
                </Chip>
              ))}
            </div>
            <div className="flex flex-wrap items-center gap-3">
              {TONES.map((t) => (
                <Marker key={t} tone={t} />
              ))}
              <Kbd>Ctrl K</Kbd>
            </div>
          </div>
        </Panel>
      </Section>

      <Section title="Forms">
        <Panel>
          <div className="grid gap-4 md:grid-cols-2">
            <Field label="Title" hint="Shown in lists and the palette.">
              {(id) => <Input id={id} placeholder="Name the task" />}
            </Field>
            <Field label="Model">
              {(id) => (
                <Combobox
                  id={id}
                  listLabel="Models"
                  value={combo}
                  onChange={setCombo}
                  options={[
                    { value: "a", label: "claude-opus-5-5" },
                    { value: "b", label: "claude-sonnet-5-5" },
                    { value: "c", label: "claude-haiku-5-5" },
                  ]}
                />
              )}
            </Field>
            <Field label="Prompt">{(id) => <Textarea id={id} rows={3} placeholder="What should the agent do?" />}</Field>
            <FieldGroup label="View" hint="A labelled group around a Segmented control.">
              <div>
                <Segmented
                  label="View"
                  value={seg}
                  onChange={setSeg}
                  options={[
                    { value: "open", label: "Open" },
                    { value: "closed", label: "Closed" },
                    { value: "all", label: "All" },
                  ]}
                />
              </div>
            </FieldGroup>
            <FieldGroup label="Events">
              <ChoiceRow control={<Checkbox checked={checked} onChange={(e) => setChecked(e.currentTarget.checked)} />} detail="waits on you">
                Needs input
              </ChoiceRow>
              <ChoiceRow control={<Checkbox disabled />} detail="run finished">
                Done (disabled)
              </ChoiceRow>
            </FieldGroup>
            <FieldGroup label="Workspace">
              <ChoiceRow control={<Radio name="dev-ui-radio" checked={radio === "a"} onChange={() => setRadio("a")} />}>Project root</ChoiceRow>
              <ChoiceRow control={<Radio name="dev-ui-radio" checked={radio === "b"} onChange={() => setRadio("b")} />} detail="auto · same path">
                New worktree
              </ChoiceRow>
            </FieldGroup>
          </div>
        </Panel>
      </Section>

      <Section title="Feedback">
        <div className="flex flex-col gap-3">
          <Alert tone="need">Two tasks need your input.</Alert>
          <Alert tone="plain" actions={<Button small>Undo</Button>}>
            Saved.
          </Alert>
          <ErrorText error={new Error("T3 is not reachable. Check the connection in Settings.")} />
          <InlineError>launch failed: worktree path already exists</InlineError>
          {confirming ? (
            <ConfirmInline danger confirmLabel="Delete" onConfirm={() => setConfirming(false)} onCancel={() => setConfirming(false)}>
              Delete snippet "review"? This cannot be undone.
            </ConfirmInline>
          ) : (
            <div>
              <Button variant="danger" onClick={() => setConfirming(true)}>
                Delete
              </Button>
            </div>
          )}
          <Panel title="Tasks" meta="0" flush>
            <EmptyState inset action={<Button small>New task</Button>}>
              No tasks in this project yet.
            </EmptyState>
          </Panel>
          <Muted>Muted paragraph text.</Muted>
          <Hint>Hint text under a control, in Geist.</Hint>
        </div>
      </Section>

      <Section title="Loading">
        <div className="grid gap-3 md:grid-cols-2">
          <Panel title="Rows" flush>
            <SkeletonRows />
          </Panel>
          <Panel title="Custom">
            <Skeleton label="Loading task">
              <div className="flex flex-col gap-2">
                <SkeletonBar className="h-4 w-2/3" />
                <SkeletonBar className="h-control-sm w-20" />
              </div>
            </Skeleton>
          </Panel>
        </div>
      </Section>

      <Section title="Lists and rows">
        <Panel title="Needs input" meta="2" flush>
          <RowList>
            <Row need className="flex flex-col gap-0.5">
              <span className="flex items-baseline gap-1.5 text-ui">
                <IdTag id={327} />
                <TextLink to="/tasks/327">Second adapter design doc</TextLink>
              </span>
              <MetaLine>
                {"T3"}
                {"Harness default"}
                <RelTime at={SAMPLE_AT} />
              </MetaLine>
            </Row>
            <Row className={needEdgeClass(false)}>
              <span className="flex items-baseline gap-1.5 text-ui">
                <IdTag id={318} />
                <span>A display row without the edge keeps its alignment</span>
              </span>
            </Row>
          </RowList>
        </Panel>
        <Panel title="Pick" flush>
          <RowList>
            <li>
              <RowButton selected>Selected row</RowButton>
            </li>
            <li>
              <RowButton>Row button</RowButton>
            </li>
            <li>
              <RowButton disabled>Disabled row</RowButton>
            </li>
          </RowList>
        </Panel>
      </Section>

      <Section title="Requests" meta="components/Requests.tsx · a session's approvals and questions">
        <Panel title="Requests" meta="3 pending" flush>
          <RequestCard request={SAMPLE_APPROVAL} harness="Claude Code" onRespond={noop} />
          <RequestCard request={SAMPLE_ALWAYS} harness="Claude Code" onRespond={noop} />
          <RequestCard request={SAMPLE_QUESTION} harness="Claude Code" onRespond={noop} />
        </Panel>
        <Panel title="Completed" flush>
          <RequestCard request={SAMPLE_APPROVAL} harness="Claude Code" outcome="answered" onRespond={noop} />
          <RequestCard request={{ ...SAMPLE_APPROVAL, id: "r4" }} harness="Claude Code" outcome="expired" onRespond={noop} />
          <RequestCard request={{ ...SAMPLE_APPROVAL, id: "r5" }} harness="Claude Code" busy onRespond={noop} />
        </Panel>
        <Panel title="Row answers" flush>
          <RowList>
            <Row need className="flex flex-col gap-1">
              <span className="flex items-baseline gap-1.5 text-ui">
                <IdTag id={375} />
                <TextLink to="/tasks/375">Answer approvals and questions in Stage</TextLink>
              </span>
              <RowRequestLine action={rowRequestAction([SAMPLE_APPROVAL])} />
              <span className="flex flex-wrap gap-1.5">
                <RowRequestControls action={rowRequestAction([SAMPLE_APPROVAL])} taskId={375} onRespond={noop} />
              </span>
            </Row>
            <Row need className="flex flex-col gap-1">
              <span className="flex items-baseline gap-1.5 text-ui">
                <IdTag id={376} />
                <TextLink to="/tasks/376">A question or several requests link to the task</TextLink>
              </span>
              <span className="flex flex-wrap gap-1.5">
                <RowRequestControls action={rowRequestAction([SAMPLE_QUESTION])} taskId={376} onRespond={noop} />
                <RowRequestControls action={rowRequestAction([SAMPLE_APPROVAL, SAMPLE_ALWAYS])} taskId={376} onRespond={noop} />
              </span>
            </Row>
          </RowList>
        </Panel>
      </Section>

      <Section title="Follow-up" meta="components/FollowUp.tsx · a message into the session">
        <Alert tone="need">{replyPrompt("Claude Code")}</Alert>
        <FollowUpDemo view={{ placement: "lead", title: "Reply", busy: false }} />
        <FollowUpDemo view={{ placement: "after-reply", title: "Follow up", busy: false }} initial="Also add a test for the empty case." />
        <FollowUpDemo view={{ placement: "after-reply", title: "Follow up", busy: true }} initial="Then open a PR." sentAt="2026-11-25T07:00:00Z" />
        <FollowUpDemo view={{ placement: "after-reply", title: "Follow up", busy: false }} initial="Run the live test again." error={BUSY_ERROR} />
      </Section>

      <Section title="Session actions" meta="components/SessionToolbarActions.tsx · declared, beside Open and Stop">
        <SessionActionsDemo actions={SAMPLE_SESSION_ACTIONS} />
        <SessionActionsDemo actions={SAMPLE_SESSION_ACTIONS.map((a) => a.id === "settle"
          ? { ...a, availability: { available: false, reason: "Already settled in T3." } } : a)} />
      </Section>

      <Section title="Harness" meta="read from capabilities · never by harness">
        <HarnessFieldsDemo />
        <Panel title="Capabilities" meta="declared by Codex" flush>
          <CapabilityList operations={SAMPLE_CAPS.operations} name="Codex" />
        </Panel>
        <Panel title="Capabilities" meta="declared by Codex" flush>
          <CapabilityList operations={{}} name="Codex" />
        </Panel>
        <Panel title="Codex session">
          <LaunchMeta parts={launchParts(SAMPLE_CONFIG, SAMPLE_CAPS)} reported={{ branch: "codex/fix-empty-state", directory: "~/src/zero/.worktrees/fix-empty-state" }} harness="Codex" />
        </Panel>
      </Section>

      <Section title="Layout">
        <Panel title="Panel" meta="meta" actions={<Button small variant="ghost">Action</Button>}>
          <div className="flex flex-col gap-3">
            <SubHeading title="Sub heading" meta="1" />
            <Details>
              <Detail label="Model">claude-opus-5-5</Detail>
              <Detail label="Branch">main</Detail>
            </Details>
            <DisclosureButton open={!folded} onClick={() => setFolded((f) => !f)} className="font-mono text-meta text-muted hover:text-fg">
              21 quiet projects
            </DisclosureButton>
            {folded ? null : <Muted>Folded content.</Muted>}
          </div>
        </Panel>
        <Panel title="Collapsed" collapsed>
          {null}
        </Panel>
      </Section>

      <Section title="Text">
        <Panel>
          <div className="flex flex-col gap-2 text-small">
            <Meta>octo · claude-opus-5-5 · 14m ago</Meta>
            <MetaLine>
              {"completed 3h ago"}
              {"ran 35m"}
              {""}
              <TextLink to="/trees" variant="rest">
                Tool roadmap
              </TextLink>
            </MetaLine>
            <span>
              <TextLink to="/tasks">Hover link</TextLink> · <TextLink to="/tasks" variant="rest">Resting link</TextLink> ·{" "}
              <TextLink to="/tasks" variant="muted">
                Muted link
              </TextLink>{" "}
              · <ExternalLink href="https://example.com">External</ExternalLink>
            </span>
            <span>
              <Highlight text="search_sessions tool" range={{ start: 7, end: 15 }} />
            </span>
          </div>
        </Panel>
      </Section>

      <Section title="Copy">
        <Panel>
          <div className="flex flex-col gap-3">
            <div>
              <CopyButton text="stage worker" label="Copy" />
            </div>
            <CommandBox command="t3 pair" />
          </div>
        </Panel>
      </Section>

      <Section title="Overlays">
        <Panel>
          <OverlayDemos />
        </Panel>
        <div className="border border-line">
          <KeyHints
            hints={[
              ["↑↓", "move"],
              ["↵", "select"],
              ["Esc", "close"],
            ]}
          >
            trailing status
          </KeyHints>
        </div>
      </Section>
    </div>
  );
}
