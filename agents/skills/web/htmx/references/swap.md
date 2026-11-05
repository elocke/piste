# Swap Strategies

## Values

| Strategy | Description |
|----------|-------------|
| `innerHTML` | Replace inner content (default) |
| `outerHTML` | Replace entire element |
| `textContent` | Replace text content (no HTML parsing) |
| `beforebegin` | Insert before element |
| `afterbegin` | Insert at start of element |
| `beforeend` | Append to end of element |
| `afterend` | Insert after element |
| `delete` | Remove element |
| `none` | Do nothing with response |

## Swap Modifiers

| Modifier | Purpose | Example |
|----------|---------|---------|
| `swap:<time>` | Delay before swap (for exit animations) | `swap:1s` |
| `settle:<time>` | Delay before settling (for enter animations) | `settle:200ms` |
| `transition:true` | Use View Transitions API | `transition:true` |
| `ignoreTitle` | Don't update page title | `ignoreTitle` |
| `scroll:top` | Scroll target after swap | `scroll:top` |
| `show:top` | Scroll element into view | `show:top` |
| `focus-scroll:true` | Scroll focused element into view | `focus-scroll:true` |

## Best Practices

- Choose swap based on UI intention
- Avoid replacing large containers unnecessarily
- Use `outerHTML` when the element identity changes (e.g., edit/view toggle)
- Use `innerHTML` when updating content within a stable container
- Use `beforeend` for appending items to lists
- Use `delete` + empty 200 response for removal
- Use `none` for side-effect-only requests (e.g., triggering server events)
- Remember: `outerHTML` on `<body>` auto-converts to `innerHTML`
