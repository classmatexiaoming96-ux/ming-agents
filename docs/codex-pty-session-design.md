# Codex Adapter PTY Session Design

## Protocol Findings

Codex CLI `0.128.0` enters an interactive TUI when started as `codex` without a subcommand. With `--no-alt-screen`, startup still emits terminal capability queries before rendering useful text:

- `ESC[6n` cursor position query.
- `ESC[c` primary device attributes query.
- `OSC 10 ; ? ST` foreground color query.

A plain PTY does not answer these automatically, so the session startup loop must write minimal responses or Codex can stall before the prompt appears. After those responses, the normalized screen contains stable text such as `OpenAI Codex` and the input prompt `›`. There is no single dedicated ready line.

Bracketed paste works for multi-line input: send `ESC[200~`, the prompt, `ESC[201~`, then carriage return. Completion detection with a UUID sentinel is viable when the instruction asks Codex to print the sentinel on its own line at the end of the answer. `PTYReader` normalization strips ANSI control sequences while preserving the rendered response text.

## Architecture

Add `CodexSession` and `CodexSessionManager` parallel to the Claude implementation:

- `CodexSession` owns the `exec.Cmd`, PTY file, `PTYReader`, send mutex, lifecycle state, and wait channel.
- `CodexSessionManager` stores sessions by `workDir` and uses a `starting` map to share concurrent startup for the same directory.
- Startup launches `codex --no-alt-screen`, disables PTY echo, starts `PTYReader`, auto-responds to terminal queries, detects auth/trust output, and waits for a debounced ready pattern.
- `SendPrompt` serializes sends, writes bracketed paste prompt plus a sentinel instruction, waits for the sentinel from the post-send offset, strips prompt echo, and returns text before the sentinel.
- `Close` closes the reader/PTY and terminates the child process with SIGTERM then SIGKILL after a short timeout.

## Adapter Flow

`CodexAdapter.Invoke` keeps its public interface. It first tries PTY session mode through a manager keyed by command/config and session keyed by `workDir`. If session startup or send fails, it falls back to the existing one-shot `codex exec <prompt>` path. Timeouts close the session before fallback/result return so a poisoned session is not reused.

## Testing

Tests use shell fake Codex commands under PTY so they do not call the real Codex service:

- session lifecycle and startup terminal query responses;
- manager session reuse by `workDir`;
- bracketed paste and sentinel completion extraction;
- adapter PTY path preserving memory across prompts;
- fallback to plain pipe when PTY startup fails.
