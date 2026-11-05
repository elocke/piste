# Trigger Syntax

## Default Triggers
- Most elements: `click`
- `<input>`, `<textarea>`, `<select>`: `change`
- `<form>`: `submit`

## Common Trigger Examples

- `click` — standard click
- `change` — value changed
- `submit` — form submission
- `load` — when element loads
- `revealed` — scrolled into viewport
- `intersect` — IntersectionObserver-based
- `keyup changed delay:500ms` — debounced keyboard input
- `input changed delay:500ms` — debounced text input
- `every 10s` — polling interval
- `keyup[key=='Enter']` — filtered keyboard event
- `click[ctrlKey]` — modifier key filter
- `click, keyup[altKey&&shiftKey&&key=='D'] from:body` — multiple triggers with keyboard shortcut

## Best Practices

- Always debounce expensive triggers (search, validation)
- Prefer SSE over polling when real-time updates are needed
- Use `once` modifier for one-time loads
- Use `changed` to avoid redundant requests
- Use `throttle` for scroll/resize events
- Combine `delay` with `changed` for input fields

## Modifier Reference

| Modifier | Purpose |
|----------|---------|
| `once` | Fire only once |
| `changed` | Only if value changed |
| `delay:<time>` | Debounce |
| `throttle:<time>` | Throttle |
| `from:<selector>` | Listen on different element |
| `target:<selector>` | Filter by event target |
| `consume` | Stop propagation |
| `queue:<strategy>` | Queue behavior (first/last/all/none) |
