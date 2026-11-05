# Anti-Patterns

## JSON Instead of HTML
Returning JSON and building DOM client-side defeats the purpose of htmx. The server should always return HTML fragments for htmx requests.

## SPA State Management
Recreating React/Vue-like client-side state management. Keep state on the server. Use sessions or database, not client-side stores.

## Full Layout in Fragment
Returning `<html>...</html>` for an htmx request. Always detect the `HX-Request` header and return only the fragment.

## No History Handling
Ignoring `hx-push-url` for navigation-like actions (pagination, tab switching, filters). Users expect the back button to work.

## Polling Abuse
Using `every 1s` unnecessarily. Prefer SSE for real-time data. If polling is needed, use reasonable intervals (5s+) and stop when not needed.

## Validation Redirect
Redirecting on validation error instead of re-rendering the form with error messages and a 422 status code.

## Not Using OOB
Updating multiple UI areas via separate JavaScript calls instead of using `hx-swap-oob` in the response to update everything in one request.

## Replacing Large Containers
Targeting a large parent container when only a small child needs updating. Target the smallest possible element.

## Missing Loading States
Not providing visual feedback during requests. Use `hx-indicator` and `hx-disabled-elt` for user feedback.

## Client-Side Templating
Building HTML strings in JavaScript and inserting them manually. Let the server render all HTML.

## Mixing Frameworks
Using htmx alongside React/Vue/Angular for the same UI concerns. Pick one paradigm. htmx is for server-rendered HTML; SPAs are for client-rendered JSON.
