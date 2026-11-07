declare module 'claude-code/testing' {
  import type { AgentLoop } from 'claude-code';
  import type { Args } from 'claude-code';
  import type { Chunk } from 'claude-code';
  import type { ClassicEventName } from 'claude-code';
  import type { ClassicEventOf } from 'claude-code';
  import type { ClassicResultOf } from 'claude-code';
  import type { ClientKeyEvent } from 'claude-code';
  import type { ClientPointerEvent } from 'claude-code';
  import type { Elements } from 'claude-code';
  import type { EventCalls } from 'claude-code';
  import type { EventName } from 'claude-code';
  import type { HookStream } from 'claude-code';
  import type { JsonValue } from 'claude-code';
  import type { On } from 'claude-code';
  import type { PluginOptions } from 'claude-code';
  import type { PressedLink } from 'claude-code';
  import type { Register } from 'claude-code';
  import type { RenderComponent } from 'claude-code';
  import type { RenderElement } from 'claude-code';
  import type { RenderPropsOf } from 'claude-code';
  import type { RenderSurface } from 'claude-code';
  import type { RenderViewport } from 'claude-code';
  import type { ResultOf } from 'claude-code';
  import type { SessionAppendInput } from 'claude-code';
  import type { StreamingEventName } from 'claude-code';
  import type { Tier } from 'claude-code';
  import type { ToolCallArgs } from 'claude-code';
  import type { ToolCallReserved } from 'claude-code';
  import type { ToolCallResult } from 'claude-code';
  import type { UiInputArgument } from 'claude-code';
  import type { UiInputResult } from 'claude-code';
  import type { UiPressResult } from 'claude-code';
  import type { UiSelectResult } from 'claude-code';

  /**
   * A value that matches by a rule inside `toEqual` and its kin
   * (`expect.any`, `expect.objectContaining`), known by its text in a failure.
   */
  export type AsymmetricMatcher = {
      readonly text: string;
  };

  /**
   * The same checks on what a promise received settles with, each resolving
   * once the promise has settled and the check passed.
   */
  export type AsyncMatchers = {
      [K in keyof Matchers]: (...args: Parameters<Matchers[K]>) => Promise<void>;
  };

  /**
   * A classic hook event the engine raises on its own, by its own name
   * (`SessionStart`, `Stop`): all but `PreToolUse`, which rides `tool.call`.
   *
   * `$.tool.call` raises `classic.PreToolUse` as a session does: beneath every
   * plugin's `tool.call` hook and above the test's own, which a deny never
   * reaches (the call resolves errored, the reason its `text`).
   */
  export type ClassicEvent = Exclude<ClassicEventName, 'classic.PreToolUse'> extends `classic.${infer E}` ? E : never;

  /**
   * What `$.classic.<Event>` takes: the event's own fields as its hook sees
   * them on `e`, less what the engine stamps at every call site.
   *
   * `hook_event_name` is the call's; `session_id`, `transcript_path` and
   * `cwd` are the session's unless the test gives them (the test session's
   * id, `''` for no local transcript, the plugin's folder).
   */
  export type ClassicFields<E extends ClassicEvent> = Omit<ClassicEventOf[`classic.${E}`], 'hook_event_name' | 'session_id' | 'transcript_path' | 'cwd'> & Partial<Pick<ClassicEventOf[`classic.${E}`], 'session_id' | 'transcript_path' | 'cwd'>>;

  /**
   * Which `Client` of a mounted drawing an act or a read means, by the `key`
   * its element carries; optional while the drawing holds exactly one.
   */
  export type ClientScope = {
      in?: string;
  };

  /**
   * A class, as `toThrow`, `toBeInstanceOf` and `expect.any` take it.
   */
  export type Constructor = abstract new (...args: never[]) => unknown;

  /**
   * A group of tests: its name leads the title of each test declared inside,
   * and its body runs at once, while the file loads.
   *
   * @param name the group's name
   * @param body declares the group's tests
   */
  export const describe: (name: string, body: () => void) => void;

  /**
   * The element each surface-dependent act of a mounted drawing reaches: a
   * handle carries the act only where the surface's table has the element.
   *
   * `input` needs `Input` and `select` needs `Select` (every surface but
   * mobile today); `key`, `pointer`, `post`, `advance` and `resize` reach a
   * `Client` (terminal and desktop today). The rest need nothing but a tree.
   */
  export type ElementOfAct = {
      input: 'Input';
      select: 'Select';
      key: 'Client';
      pointer: 'Client';
      post: 'Client';
      advance: 'Client';
      resize: 'Client';
  };

  /**
   * What a mounted drawing's `find` and `findAll` match an element on, every
   * field given at once: its tag, its `key`, and the text it shows.
   *
   * `text` matches the element's whole shown text (string children, a Button
   * or Link's label, a Markdown's text, a Code's source, an Input's value):
   * a string by inclusion, a RegExp by test.
   *
   * @example
   * await ui.findAll({ type: 'Text', text: /overdue/, in: 'board' })
   */
  export type ElementQuery = {
      type?: string;
      key?: string;
      text?: string | RegExp;
      /**
       * Searches what this `Client`'s surface module drew (by the `Client`'s
       * key) instead of the plugin's own tree.
       */
      in?: string;
  };

  /**
   * What a test holds as `$`, the engine's own: every call on it is made as
   * the REPL, the query loop and the render sites make theirs, over every plugin.
   *
   * `next.origin` is the engine, the chain every plugin loaded. `$.ui.mount`
   * is a surface drawing an instance; `press`, `input` and `select` a person
   * acting on an element the test drew; `$.classic` the classic hook events.
   */
  export type Engine = {
      [N in keyof EventCalls]: N extends 'ui' ? EngineNoun<N> & EnginePress & EngineInput & EngineSelect & EngineMount : EngineNoun<N>;
  } & {
      classic: EngineClassic;
  };

  /**
   * One call on the engine's `$`: the event's input whole, as an engine call
   * site passes it, to its result, or for a streaming event to its stream.
   */
  export type EngineCall<E extends EventName> = E extends StreamingEventName ? (e: Args<E>) => HookStream<Chunk<E>, ResultOf[E]> : (e: Args<E>) => Promise<ResultOf[E]>;

  /**
   * The classic (settings) hook events as the engine raises them: each a call
   * running the `classic.<Event>` chain over every plugin hooked on it.
   *
   * No settings hook runs beneath, so the test's `on('classic.<Event>')` is the
   * bottom; the envelope (`hook_event_name`, `session_id`, `transcript_path`,
   * `cwd`) is stamped unless given. `PreToolUse` rides `$.tool.call` instead.
   *
   * @example
   * await $.classic.SessionStart({ source: 'clear' })
   */
  export type EngineClassic = {
      [E in ClassicEvent]: (e: ClassicFields<E>) => Promise<ClassicResultOf[`classic.${E}`]>;
  };

  /**
   * A surface typing into an Input a test rendered: the `ui.input` chain over
   * every plugin hooked on it, the Input's own handler last.
   */
  export type EngineInput = {
      /**
       * Types into the Input, as an edit (`kind` `change`, reaching `onInput`) or
       * Enter (`submit`, the default, reaching `onSubmit`) with that text.
       *
       * Resolves once the chain, the handler and the work it started settled.
       * `e.surface` is the surface of the drawing that holds it.
       *
       * @param target whose Input, its key, the text, the kind, and the instance
       *               and surface when several hold it
       * @returns what the chain settled on; rejects when no such Input is drawn, or
       *          several are and neither instance nor surface tells them apart
       * @example
       * await $.ui.input({ plugin: 'notes', key: 'q', text: 'g', kind: 'change' })
       * await $.ui.input({ plugin: 'notes', key: 'q', text: 'groceries' })
       */
      input: (target: InputTarget) => Promise<UiInputResult | undefined>;
  };

  /**
   * A surface drawing a component instance through the plugins, then driven
   * by key through a handle typed by that surface (Mounted).
   *
   * `ui.render` raised there for those props, the tree validated by that
   * table, its `Client`s running; the reads and `press` on every surface,
   * `input` where the table has `Input`, the `Client` acts where `Client`.
   *
   * @example
   * for (const surface of SURFACES) run(await $.ui.mount({ ...HINT, surface }))
   */
  export type EngineMount = {
      /**
       * Raises the plugins' drawing of the component on the surface and holds
       * it, as that surface showing the instance does.
       *
       * Resolves once the tree validated and every `Client` in it drew once.
       *
       * @param target whose elements, the surface, component, props and requestId
       * @returns the mounted drawing; rejects when no `surface` (or an unknown
       *   one) is named, when the surface cannot draw the tree returned (with
       *   the reason: an element or a prop it lacks, a dialog's tree past its
       *   rows), or when a `Client`'s module failed or drew nothing
       */
      mount: <P extends RenderSurface, C extends RenderComponent>(target: MountTarget<P, C>) => Promise<Mounted<P, C>>;
  };

  /**
   * One noun of the engine's `$`: each of its events as the engine calls it,
   * `tool.call` typed per tool and `ui.render` per component, `ui.resolve` out.
   *
   * `skill.prompt` takes a call's input: its frontmatter and source may be
   * left out, as a test written before the event carried them does.
   */
  export type EngineNoun<N extends keyof EventCalls> = {
      [V in EngineNounEvent<N>]: `${N}.${V}` extends 'tool.call' ? EngineToolCall : `${N}.${V}` extends 'ui.render' | 'skill.prompt' ? EventCalls[N][V] : EngineCall<`${N}.${V}` & EventName>;
  };

  /**
   * The events of one noun a test's `$` carries: every one but `ui.resolve`,
   * which a render hook calls on its own `$` and the engine never raises.
   */
  export type EngineNounEvent<N extends keyof EventCalls> = Exclude<keyof EventCalls[N] & string, `${N}.resolve` extends 'ui.resolve' ? 'resolve' : never>;

  /**
   * A surface pressing a Button a test rendered, or a Markdown's link: the
   * `ui.press` chain over every plugin hooked on it, its own closure last.
   */
  export type EnginePress = {
      /**
       * Presses the Button, as a click or its hotkey does; with `link`, the
       * Markdown's link, as a click on it does.
       *
       * Resolves once the chain, `onPress` and any work `onPress` left running
       * unawaited have settled (work asleep on `mock.clock` waits for the test
       * to advance it). `e.surface` is the surface of the drawing that holds it.
       *
       * @param target whose element, its key, the instance and surface when
       *   several hold it, and for a Markdown the link
       * @returns what the chain settled on; rejects when no such element is
       *   drawn, or several are and neither instance nor surface tells them apart
       */
      press: (target: PressTarget) => Promise<UiPressResult | undefined>;
  };

  /**
   * A surface picking an option of a Select a test rendered: the `ui.select`
   * chain over every plugin hooked on it, the Select's own `onSelect` last.
   */
  export type EngineSelect = {
      /**
       * Picks the option, as choosing it on the surface does; resolves once the
       * chain, `onSelect` and the work it started have settled.
       *
       * `e.surface` is the surface of the drawing that holds it.
       *
       * @param target whose Select, its key, the option's value, and the
       *   instance and surface when several hold it
       * @returns what the chain settled on; rejects when no such Select is
       *   drawn, or several are and neither instance nor surface tells them apart
       * @example
       * await $.ui.select({ plugin: 'notes', key: 'sort', value: 'date' })
       */
      select: (target: SelectTarget) => Promise<UiSelectResult | undefined>;
  };

  /**
   * `$.tool.call` on a test's `$`: a call as the engine raises one, `agentId`
   * the loop it runs in (a subagent's, a teammate's); left out, the main loop.
   *
   * A hooked `tool.check` runs as for a background subagent: a test's
   * `classic.PermissionRequest` answers an ask; no test plays a person's yes. A
   * test states `isReadOnly` on `$.tool.check`; `$.ui.ask` states its own.
   *
   * @example
   * await $.tool.call({ tool: 'Bash', command: 'ls', agentId: 'a1' })
   */
  export type EngineToolCall = {
      <T extends string>(input: ToolCallArgs & Pick<ToolCallReserved<T>, 'tool'> & AgentLoop): Promise<ToolCallResult<T>>;
      (input: ToolCallArgs & AgentLoop): Promise<ToolCallResult>;
  };

  /**
   * The checks on a value (`expect(received)`), and with them the matchers
   * that stand inside an expected value (`expect.any(Number)`).
   *
   * A check that fails throws. In a hook the test registered with `on` the
   * engine skips the hook as it skips any that throws, and the test fails all
   * the same, with the check's message and the hook's event.
   */
  export type Expect = Expecting & Matching;

  /**
   * Checks a value: `expect(received).toEqual(expected)` throws an
   * AssertionError naming both sides when it fails, a message given leading.
   *
   * `.not` negates; `.resolves` and `.rejects` check what a promise settles
   * with; `expect.any(Number)` and its kin stand inside an expected value.
   */
  export const expect: Expect;

  /**
   * What `expect(received)` answers: the checks, their negation, and the
   * checks on what a promise received resolves or rejects with.
   */
  export type Expectation = Negatable<Matchers> & {
      /**
       * The checks on what the promise received resolves with; a rejection
       * fails them.
       */
      resolves: Negatable<AsyncMatchers>;
      /**
       * The checks on what the promise received rejects with; a resolution
       * fails them.
       */
      rejects: Negatable<AsyncMatchers>;
  };

  /**
   * `expect(received, message?)`: the checks on a value, a message of the
   * test's own leading a failure's.
   */
  export type Expecting = (received: unknown, message?: string) => Expectation;

  /**
   * One element of a mounted drawing as `find` returns it: the description
   * the plugin's hook (or a `Client`'s module) built, read as plain data.
   *
   * What every surface is handed to draw, not what any surface painted: the
   * same on the terminal, the desktop, VS Code and mobile.
   */
  export type FoundElement = {
      /**
       * The element's tag: `Box`, `Text`, `Button`, `Input`, `Client`, ...
       */
      type: string;
      /**
       * What its props carry as `key`, when they do.
       */
      key: string | undefined;
      /**
       * Its props, plain data: a Button's `label`, a Text's `color`, ...;
       * handlers never cross (a Button's `onPress` is not here).
       */
      props: Record<string, unknown>;
      /**
       * What it shows: its string children in document order and, for the
       * leaves that draw a prop as text, that prop (ElementQuery `text`).
       */
      text: string;
      /**
       * Its children as drawn: strings and element descriptions.
       */
      children: unknown[];
  };

  /**
   * What `$.ui.input` takes: the plugin whose hook drew the Input, its `key`,
   * the field's text, which input it is, instance and surface when several.
   */
  export type InputTarget = {
      plugin: string;
      key: string;
      /**
       * The field's whole text at that moment: `e.value` for the chain.
       */
      text: string;
      /**
       * `submit` (the default) is Enter with that text and reaches `onSubmit`;
       * `change` is one edit left in the field and reaches `onInput`.
       *
       * A person typing is a run of `change`s, then a `submit`.
       */
      kind?: UiInputArgument['kind'];
      /**
       * Which instance holds it, when the plugin drew the key in several.
       */
      requestId?: string;
      /**
       * Which surface's drawing, when the key is drawn on more than one; the
       * input carries the surface of the drawing it lands in either way.
       */
      surface?: RenderSurface;
  };

  /**
   * The checks `expect(received)` offers; each throws an AssertionError when
   * it fails, naming what was expected and what was received.
   */
  export type Matchers = {
      /**
       * Passes when received is the value, by `Object.is`.
       *
       * @param expected the value
       */
      toBe: (expected: unknown) => void;
      /**
       * Passes when received equals the value in structure, properties holding
       * undefined ignored; `expect.any` and its kin match inside it.
       *
       * @param expected the value
       */
      toEqual: (expected: unknown) => void;
      /**
       * As `toEqual`, but undefined properties and prototypes count.
       *
       * @param expected the value
       */
      toStrictEqual: (expected: unknown) => void;
      /**
       * Passes when received holds at least the object's properties, equal.
       *
       * @param expected the object
       */
      toMatchObject: (expected: object) => void;
      /**
       * Passes when a string received includes the item, or an iterable
       * received has an element that is it.
       *
       * @param item the substring or element
       */
      toContain: (item: unknown) => void;
      /**
       * Passes when an iterable received has an element equal to the item.
       *
       * @param item the element
       */
      toContainEqual: (item: unknown) => void;
      /**
       * Passes when received's `length` is the number.
       *
       * @param length the number
       */
      toHaveLength: (length: number) => void;
      /**
       * Passes when received holds a property at the path (`a.b`, or the keys
       * in order), equal to the value when one is given.
       *
       * @param path the property's path
       * @param value what it must equal, when given
       */
      toHaveProperty: (path: string | readonly string[], value?: unknown) => void;
      /**
       * Passes when received is undefined.
       */
      toBeUndefined: () => void;
      /**
       * Passes when received is anything but undefined.
       */
      toBeDefined: () => void;
      /**
       * Passes when received is null.
       */
      toBeNull: () => void;
      /**
       * Passes when received is truthy.
       */
      toBeTruthy: () => void;
      /**
       * Passes when received is falsy.
       */
      toBeFalsy: () => void;
      /**
       * Passes when received is NaN.
       */
      toBeNaN: () => void;
      /**
       * Passes when received is greater than the bound.
       *
       * @param bound the bound
       */
      toBeGreaterThan: (bound: number | bigint) => void;
      /**
       * Passes when received is the bound or greater.
       *
       * @param bound the bound
       */
      toBeGreaterThanOrEqual: (bound: number | bigint) => void;
      /**
       * Passes when received is less than the bound.
       *
       * @param bound the bound
       */
      toBeLessThan: (bound: number | bigint) => void;
      /**
       * Passes when received is the bound or less.
       *
       * @param bound the bound
       */
      toBeLessThanOrEqual: (bound: number | bigint) => void;
      /**
       * Passes when a string received includes the text, or the pattern matches
       * it.
       *
       * @param pattern the text or pattern
       */
      toMatch: (pattern: string | RegExp) => void;
      /**
       * Passes when a string received starts with the prefix.
       *
       * @param prefix the prefix
       */
      toStartWith: (prefix: string) => void;
      /**
       * Passes when a string received ends with the suffix.
       *
       * @param suffix the suffix
       */
      toEndWith: (suffix: string) => void;
      /**
       * Passes when received is an instance of the class.
       *
       * @param expected the class
       */
      toBeInstanceOf: (expected: Constructor) => void;
      /**
       * Passes when calling received throws (or, under `rejects`, the rejection
       * is) an error as described: by substring, pattern, class or message.
       *
       * @param expected the description; any throw passes without one
       */
      toThrow: (expected?: ThrowExpectation) => void;
  };

  /**
   * The matchers that stand inside an expected value, each matching received
   * there by a rule instead of by equality.
   */
  export type Matching = {
      /**
       * Matches any instance of the class, a primitive by its wrapper
       * (`expect.any(Number)`).
       *
       * @param expected the class
       * @returns the matcher
       */
      any: (expected: Constructor) => AsymmetricMatcher;
      /**
       * Matches anything but null and undefined.
       *
       * @returns the matcher
       */
      anything: () => AsymmetricMatcher;
      /**
       * Matches a string that includes the text.
       *
       * @param text the substring
       * @returns the matcher
       */
      stringContaining: (text: string) => AsymmetricMatcher;
      /**
       * Matches a string the pattern matches.
       *
       * @param pattern the pattern, or the source of one
       * @returns the matcher
       */
      stringMatching: (pattern: string | RegExp) => AsymmetricMatcher;
      /**
       * Matches an object holding at least the shape's properties, equal.
       *
       * @param shape the properties
       * @returns the matcher
       */
      objectContaining: (shape: object) => AsymmetricMatcher;
      /**
       * Matches an array holding an element equal to each of the items.
       *
       * @param items the elements
       * @returns the matcher
       */
      arrayContaining: (items: readonly unknown[]) => AsymmetricMatcher;
  };

  /**
   * The world beneath the plugins, mocked noun by noun: each member registers
   * hooks of the test's on `on`, visible where the test calls it.
   */
  export type Mock = {
      /**
       * Answers `$.clock` from a clock in memory that moves only when the test
       * moves it: `clock.now` reads it, and each wait is held.
       *
       * A held wait resolves when an advance crosses the time it is due, and is
       * dropped when its dispatch aborts; one held past a hook's budget (ten
       * seconds of real time) is let go, as a hook that overran.
       *
       * @param on the test's `on`
       * @param options where the clock starts (`now`, 0 when not given)
       * @returns the clock: its time, and the calls that move it
       */
      clock: (on: On, options?: MockClockOptions) => MockClock;
      /**
       * Answers `$.store` from a store in memory: `get`, `set`, `delete` and
       * `keys` over it, as the engine keeps a plugin's own.
       *
       * @param on the test's `on`
       * @param entries what the store holds at the start (nothing when not given)
       */
      store: (on: On, entries?: Readonly<Record<string, unknown>>) => void;
      /**
       * Answers `$.env.get` from a set of variables; one not listed is unset.
       *
       * @param on the test's `on`
       * @param variables the environment the plugins read
       */
      env: (on: On, variables: Readonly<Record<string, string>>) => void;
      /**
       * Records what is appended to the session's conversations: each row the
       * kit stored for a plugin's `$.session.append`, or for the test's own.
       *
       * The kit answers with or without this, as a session stores the row: a
       * plugin's minted or refused, a raised one's pinned blocks put back. This is
       * the test's `session.append` hook: one more needs a matcher (`{ door }`).
       *
       * @param on the test's `on`
       * @returns the session: the rows appended so far
       */
      session: (on: On) => MockSession;
  };

  /**
   * The world beneath the plugins, mocked noun by noun: `mock.clock`,
   * `mock.store`, `mock.env` and `mock.session`.
   *
   * Each registers hooks of the test's on the `on` it is handed, visible where
   * the test calls it, and answers its noun from memory or records it.
   */
  export const mock: Mock;

  /**
   * The clock `mock.clock` hands back: the time its hooks answer, and the only
   * ways it moves.
   */
  export type MockClock = {
      /**
       * The time now, in milliseconds: what `$.clock.now()` resolves beneath the
       * plugins.
       *
       * @returns the time
       */
      now: () => number;
      /**
       * Moves the clock on, resolving each wait due on the way (`$.clock.sleep`,
       * `after`, `every`, this clock's `sleep`) in the order it comes due.
       *
       * The clock reads each wait's time as it resolves, and what one started
       * runs before the next resolves.
       *
       * @param ms how far to move, in milliseconds
       * @returns resolves once the clock is there and the event loop settled
       */
      advance: (ms: number) => Promise<void>;
      /**
       * Moves the clock to a time at or past now, as `advance` would.
       *
       * @param ms the time, in milliseconds
       * @returns resolves once the clock is there and the event loop settled
       */
      set: (ms: number) => Promise<void>;
      /**
       * Lets what is already under way run as far as it can without the clock
       * moving: every wait due now resolves and the event loop settles.
       *
       * The same as `advance(0)`; the name for the step between starting a
       * dispatch unawaited and looking at what it did.
       *
       * @returns resolves once the event loop settled, the time where it was
       */
      settle: () => Promise<void>;
      /**
       * Resolves once the clock has moved this far past now: how a hook of the
       * test's answers late.
       *
       * @param ms how far, in milliseconds
       * @returns resolves when an advance crosses that time
       */
      sleep: (ms: number) => Promise<void>;
  };

  /**
   * Where a mocked clock starts: `now`, in milliseconds (0 when not given).
   */
  export type MockClockOptions = {
      now?: number;
  };

  /**
   * The session `mock.session` hands back: what was appended to its
   * conversations while the test ran.
   */
  export type MockSession = {
      /**
       * The rows stored so far, oldest first, each as the event carried it:
       * `message` as stored, `door`, `origin`, `uuid` and `agentId`.
       *
       * A plugin's own row has the door `note`, the plugin as `origin` and a
       * fresh `uuid`; a row a hook above refused, or the kit, is left out.
       *
       * @returns the rows
       */
      appended: () => readonly SessionAppendInput[];
  };

  /**
   * A drawing of component `C` the test mounted on surface `P`: reads over its
   * description, acts on its elements by key, `Client` acts where `P` has one.
   *
   * Narrowed by the `surface` the test passed, as `e.surface` narrows
   * `$.ui.resolve(e)`: no `input` on `mobile`, no `key` on `vscode`, whose
   * tables (Elements) lack `Input` and `Client`; over a union, their meet.
   */
  export type Mounted<P extends RenderSurface = RenderSurface, C extends RenderComponent = RenderComponent> = {
      [K in keyof MountedMembers<P, C> as K extends keyof ElementOfAct ? ElementOfAct[K] extends keyof Elements[P] ? K : never : K]: MountedMembers<P, C>[K];
  };

  /**
   * Every member a mounted drawing of component `C` can have; `Mounted<P, C>`
   * keeps the ones surface `P` has the element for (ElementOfAct).
   *
   * Each act resolves once the work it started settled (chain, handler, what
   * the handler left running) and what a plugin invalidated was drawn again.
   * The kit exercises the mod's hooks and modules, never a surface's paint.
   */
  export type MountedMembers<P extends RenderSurface, C extends RenderComponent = RenderComponent> = {
      /**
       * Where this drawing is, as the test named it.
       */
      readonly surface: P;
      /**
       * The tree as last drawn: what the plugins' `ui.render` chain returned
       * for this instance, validated by the surface's table, as plain data.
       *
       * With `in`, what that `Client`'s surface module last drew instead.
       *
       * @param scope `{ in }`: a `Client`'s key, to read its module's tree
       * @returns the tree; rejects with the refusal when the surface could not
       *   draw what the chain returned, or with a `Client`'s fault line
       * @example
       * expect(await ui.drawn()).toMatchObject({ type: 'Box' })
       */
      drawn: (scope?: ClientScope) => Promise<RenderElement>;
      /**
       * The first element matching the query, outermost first in document
       * order, or undefined.
       *
       * @param query tag, key and shown text to match; `in` to search a Client
       * @returns the element's description, or undefined when none matches
       * @example
       * expect((await ui.find({ key: 'count' }))?.text).toBe('3 notes')
       */
      find: (query: ElementQuery) => Promise<FoundElement | undefined>;
      /**
       * Every element matching the query, in document order.
       *
       * @param query tag, key and shown text to match; `in` to search a Client
       * @returns the matching elements' descriptions
       * @example
       * expect(await ui.findAll({ type: 'Button' })).toHaveLength(2)
       */
      findAll: (query: ElementQuery) => Promise<FoundElement[]>;
      /**
       * Presses the Button keyed `key`, as the person activating it on this
       * surface does: the `ui.press` chain, the Button's own `onPress` last.
       *
       * `e.surface` is this drawing's. With `link`, presses that link of the
       * Markdown keyed `key` instead. A Button a `Client`'s module drew is
       * pressed the same way, by its key.
       *
       * @param target the key; another plugin's name to press its Button; a link
       * @returns what the chain settled on
       * @example
       * await ui.press({ key: 'save' })
       */
      press: (target: MountPressTarget) => Promise<UiPressResult | undefined>;
      /**
       * Types into the Input keyed `key`: the `ui.input` chain with the text as
       * `e.value`, the Input's own handler last.
       *
       * `kind` `submit` (the default) is Enter with that text, `change` an edit
       * that leaves it in the field.
       *
       * @param target the key and the text; `kind`; another plugin's name
       * @returns what the chain settled on
       * @example
       * await ui.input({ key: 'title', text: 'groceries' })
       */
      input: (target: MountInputTarget) => Promise<UiInputResult | undefined>;
      /**
       * Picks `value` in the Select keyed `key`: the `ui.select` chain, the
       * Select's own `onSelect` last.
       *
       * @param target the key and the option's value; another plugin's name
       * @returns what the chain settled on
       * @example
       * await ui.select({ key: 'sort', value: 'date' })
       */
      select: (target: MountSelectTarget) => Promise<UiSelectResult | undefined>;
      /**
       * Hands a `Client`'s `onKey` listener one key, as a key pressed while
       * its region has the focus does.
       *
       * `in` names the `Client` by key; optional when the drawing holds one.
       *
       * @param event the key (`{ key: 'right' }`, `{ key: 'a', ctrl: true }`)
       * @example
       * await ui.key({ key: 'return', in: 'board' })
       */
      key: (event: MountKeyEvent) => Promise<void>;
      /**
       * Hands a `Client`'s `onPointer` listener one event, in the cells of its
       * region as every surface that draws a `Client` reports them.
       *
       * @param event the event (`{ type: 'down', x: 3, y: 0, button: 'left' }`)
       * @example
       * await ui.pointer({ type: 'down', x: 2, y: 0, button: 'left' })
       */
      pointer: (event: MountPointerEvent) => Promise<void>;
      /**
       * Posts as a `Client`'s own `surface.post(data)` does: the plugin's
       * `ui.message` hooks run with `e.data`, `e.surface` this drawing's.
       *
       * A `{ props }` they answer reaches the instance, which redraws with
       * its state kept.
       *
       * @param data plain data (JsonValue)
       * @param scope `{ in }`: which `Client`, when the drawing holds several
       * @example
       * await ui.post({ pick: 2 })
       */
      post: (data: JsonValue, scope?: ClientScope) => Promise<void>;
      /**
       * Moves the drawing's frame clock on: each `surface.every(ms, fn)` timer
       * of its `Client`s fires at every interval of its own the move crosses.
       *
       * @param ms how far, in milliseconds
       * @example
       * await ui.advance(250)
       */
      advance: (ms: number) => Promise<void>;
      /**
       * Lays a `Client`'s region out at a size, as the surface measuring it
       * does: what its module reads as `surface.columns` and `surface.rows`.
       *
       * @param size cells across and down; `in`: which `Client`
       * @example
       * await ui.resize({ columns: 40, rows: 3 })
       */
      resize: (size: MountResizeTarget) => Promise<void>;
      /**
       * Draws the instance again, as the engine does when its props change (a
       * tool row's output arrived): the `ui.render` chain with the next props.
       *
       * A plugin's own `$.ui.invalidate` needs no call: the drawing follows it
       * before the next read.
       *
       * @param props the component's next props; the current ones when absent
       * @example
       * await ui.redraw({ ...TOOL_ROW, isRunning: false, output: 'done' })
       */
      redraw: (props?: RenderPropsOf[C]) => Promise<void>;
      /**
       * Lets the drawing go, as the surface dropping the instance does: its
       * `Client`s and their timers end. Later acts on the handle reject.
       *
       * @example
       * await ui.unmount()
       */
      unmount: () => Promise<void>;
  };

  /**
   * What a mounted drawing's `input` takes: the Input's key, the text, which
   * of the two inputs it is (`submit` when unsaid), another plugin's name.
   */
  export type MountInputTarget = {
      key: string;
      text: string;
      kind?: UiInputArgument['kind'];
      plugin?: string;
  };

  /**
   * What a mounted drawing's `key` takes: the key as a `Client`'s `onKey`
   * listener receives it (ClientKeyEvent), and which `Client` when several.
   */
  export type MountKeyEvent = ClientKeyEvent & ClientScope;

  /**
   * What a mounted drawing's `pointer` takes: the event as a `Client`'s
   * `onPointer` listener receives it, in its region's cells, and which one.
   */
  export type MountPointerEvent = ClientPointerEvent & ClientScope;

  /**
   * What a mounted drawing's `press` takes: the Button's key, another
   * plugin's name when the Button is not the mounted plugin's, a link.
   */
  export type MountPressTarget = {
      key: string;
      plugin?: string;
      /**
       * Presses this link of the Markdown keyed `key` instead of a Button.
       */
      link?: PressedLink;
  };

  /**
   * What a mounted drawing's `resize` takes: a `Client` region's size in
   * cells, as the surface measuring it hands it, and which `Client`.
   */
  export type MountResizeTarget = {
      columns: number;
      rows: number;
      in?: string;
  };

  /**
   * What a mounted drawing's `select` takes: the Select's key, the picked
   * option's value, another plugin's name when the Select is not its own.
   */
  export type MountSelectTarget = {
      key: string;
      value: string;
      plugin?: string;
  };

  /**
   * What `$.ui.mount` takes: whose elements the test will act on, the surface
   * that draws, and the component instance the engine asks the plugins for.
   *
   * The envelope a `ui.render` hook sees as `e` (`surface`, `component`,
   * `requestId`, `viewport`, `props`) plus `plugin`. `surface` is never
   * defaulted: one body run over several surfaces is what shows independence.
   *
   * @example
   * $.ui.mount({ plugin: 'notes', surface, component: 'Pane', props: PANE })
   */
  export type MountTarget<P extends RenderSurface = RenderSurface, C extends RenderComponent = RenderComponent> = {
      /**
       * Whose elements the handle's acts address by bare `key`: the plugin
       * under test, usually. An act names another plugin to reach its element.
       */
      plugin: string;
      /**
       * What draws: its element table validates every tree the plugins
       * return, and every dispatch the handle raises carries it as `e.surface`.
       *
       * One of `terminal`, `desktop`, `vscode`, `mobile`.
       */
      surface: P;
      /**
       * Which component the engine asks the plugins to draw (`Pane`,
       * `AbovePrompt`, `ToolUse`, ...): what a `ui.render` matcher narrows on.
       */
      component: C;
      /**
       * The component's props, what the hook reads as `e.props`.
       */
      props: RenderPropsOf[C];
      /**
       * The instance drawn (a pane's id, a tool row's tool_use_id); one is
       * minted when absent. Two mounts of one component are two instances.
       */
      requestId?: string;
      /**
       * What the surface measured, as the hook reads it under `e.viewport`;
       * absent when the surface has not measured, as on `e`.
       *
       * Cells across and down in the surface's monospace metric and whether it
       * docks a pane. A `Client` in the drawing starts laid out at this size
       * (0 by 0 when absent) until the handle's `resize` lays its region out.
       */
      viewport?: RenderViewport;
  };

  /**
   * A set of checks and, under `not`, the same set passing where they fail.
   */
  export type Negatable<M> = M & {
      /**
       * The checks negated.
       */
      not: M;
  };

  /**
   * Written inline in a test and loaded as a plugin folder is: its name, the
   * tier it loads in (`user` when not given), and its hooks module's `register`.
   *
   * `register` is written `register(on) { ... }` and is self-contained, as a
   * module's is: it closes over nothing of the test file.
   */
  export type Plugin = {
      name: string;
      tier?: PluginTier;
      register: Register;
  };

  /**
   * A tier a plugin loads in: every tier but the engine's own.
   */
  export type PluginTier = Exclude<Tier, 'core'>;

  /**
   * What `$.ui.press` takes: the plugin whose `ui.render` hook drew the element,
   * the `key` it gave it, the instance and surface when several, a `link`.
   */
  export type PressTarget = {
      plugin: string;
      key: string;
      /**
       * Which instance holds it, when the plugin drew the key in several.
       */
      requestId?: string;
      /**
       * Which surface's drawing, when the key is drawn on more than one; the
       * press carries the surface of the drawing it lands in either way.
       */
      surface?: RenderSurface;
      /**
       * For a Markdown answering its links: which of them the press lands on.
       */
      link?: PressedLink;
  };

  /**
   * What `$.ui.select` takes: the plugin whose hook drew the Select, its
   * `key`, the picked option's value, instance and surface when several.
   */
  export type SelectTarget = {
      plugin: string;
      key: string;
      /**
       * Which option is picked, by the `value` the Select listed it under.
       */
      value: string;
      /**
       * Which instance holds it, when the plugin drew the key in several.
       */
      requestId?: string;
      /**
       * Which surface's drawing, when the key is drawn on more than one; the
       * pick carries the surface of the drawing it lands in either way.
       */
      surface?: RenderSurface;
  };

  /**
   * One test: it passes when its body returns or resolves, and fails when it
   * throws, rejects or outlasts its time (5000 ms, or `timeoutMs`).
   *
   * The body gets the engine's `$` and an `on` whose hooks sit beneath every
   * plugin; `plugins` load inline plugins beside the one under test. A failure
   * carries what the engine reported meanwhile: each hook it skipped, and why.
   *
   * @param name the test's name, led in its title by the describes around it
   * @param rest the body, `($, on) => ...`, or the options then the body
   */
  export const test: (name: string, ...rest: TestRest) => void;

  /**
   * A test: the engine's `$`, and `on`, a plugin's registrar, whose hooks sit
   * beneath every plugin; beneath them the bottom hook throws, naming its event.
   *
   * A test registers its hooks before its first call on `$`, which loads the
   * plugins. Beneath `$.state`, a render's `$.ui.invalidate` and `session.append`
   * the kit answers. A check that fails in a hook, or a refused answer, fails it.
   */
  export type TestBody = ($: Engine, on: On) => unknown;

  /**
   * What `test` takes beside its name: the inline plugins it loads beside the
   * one under test, and how long it may run (5000 ms when not given).
   *
   * Also the plugin under test's `userConfig` values.
   */
  export type TestOptions = {
      plugins?: readonly Plugin[];
      timeoutMs?: number;
      /**
       * The plugin under test's `userConfig` values, standing as the ones
       * stored in settings.
       *
       * `register(on, options)` receives them as a load does: unlisted values
       * unset, defaults filled in, then validated, a bad required field failing
       * the load. Left out: the manifest's defaults. Inline plugins get none.
       *
       * @example
       * test('greets', { options: { greeting: 'yo' } }, body)
       */
      options?: PluginOptions;
  };

  /**
   * What follows a test's name: its body, or its options then its body.
   */
  export type TestRest = readonly [body: TestBody] | readonly [options: TestOptions, body: TestBody];

  /**
   * What `toThrow` compares the thrown error with: a substring or pattern of
   * its message, its class, a value carrying the whole message, or nothing.
   */
  export type ThrowExpectation = string | RegExp | Constructor | WithMessage;

  /**
   * Says which tier the plugin under test loads in, once, at the top of the
   * file: `prepend`, `user` (when unsaid), `append` or `builtin`.
   *
   * @param tier the tier
   */
  export const tier: (tier: PluginTier) => void;

  /**
   * An error-like value `toThrow` compares by its whole message.
   */
  export type WithMessage = {
      message: string;
  };
}

