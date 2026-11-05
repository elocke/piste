# htmx Server-Side Integration

## Table of Contents
- [Core Principle](#core-principle)
- [Request Headers](#request-headers)
- [Response Headers](#response-headers)
- [Response Handling by Status Code](#response-handling)
- [Fragment Architecture](#fragment-architecture)
- [Common Server Patterns](#common-server-patterns)
- [Framework Integration Examples](#framework-examples)
- [Quirks and Gotchas](#quirks)

---

## Core Principle

htmx servers return **HTML fragments**, not JSON. The response is swapped directly into the DOM. This is the fundamental difference from SPA/JSON API architecture.

```
Browser -> HTTP Request (with htmx headers) -> Server
Server -> HTML Fragment Response (with optional htmx headers) -> Browser
Browser -> Swaps HTML into DOM
```

Never return full HTML layout for an htmx request unless intentional.

---

## Request Headers

htmx sends these headers with every AJAX request:

| Header | Value | Usage |
|--------|-------|-------|
| `HX-Request` | `"true"` | Detect htmx requests on the server |
| `HX-Trigger` | Element ID | ID of the element that triggered the request |
| `HX-Trigger-Name` | Element name | Name attribute of triggering element |
| `HX-Target` | Element ID | ID of the target element |
| `HX-Current-URL` | URL string | Current page URL |
| `HX-Boosted` | `"true"` | Present if request is via `hx-boost` |
| `HX-History-Restore-Request` | `"true"` | Present if restoring from history cache miss |
| `HX-Prompt` | User input | User's response to `hx-prompt` dialog |

### Server-Side Detection
```python
# Python/FastAPI
if request.headers.get("HX-Request"):
    return templates.TemplateResponse("_partial.html", {"request": request})
else:
    return templates.TemplateResponse("full_page.html", {"request": request})
```

```python
# Python/Flask
if request.headers.get('HX-Request'):
    return render_template('partial.html')
else:
    return render_template('full_page.html')
```

---

## Response Headers

### HX-Trigger
Trigger client-side events from the server response.

**Simple event:**
```
HX-Trigger: myEvent
```

**Multiple events:**
```
HX-Trigger: event1, event2
```

**Events with data:**
```
HX-Trigger: {"showMessage": {"level": "info", "message": "Item saved!"}}
```

**Timing variants:**
- `HX-Trigger` — fires immediately on response receipt
- `HX-Trigger-After-Swap` — fires after DOM swap
- `HX-Trigger-After-Settle` — fires after settling

**Listening for triggered events:**
```html
<div hx-trigger="showMessage from:body"
     hx-get="/notifications"
     hx-target="this">
</div>
```

Or via JavaScript:
```javascript
document.body.addEventListener('showMessage', function(e) {
    alert(e.detail.message);
});
```

### HX-Location
Client-side redirect without full page reload (like a boosted link):
```
HX-Location: /new-page
```

With options:
```
HX-Location: {"path": "/new-page", "target": "#content", "swap": "innerHTML"}
```

### HX-Push-Url
Push URL to browser history:
```
HX-Push-Url: /items/42
```

Prevent history update:
```
HX-Push-Url: false
```

### HX-Replace-Url
Replace current URL (no new history entry):
```
HX-Replace-Url: /items/42
```

### HX-Redirect
Full page redirect (traditional browser navigation):
```
HX-Redirect: /login
```

### HX-Refresh
Full page refresh:
```
HX-Refresh: true
```

### HX-Reswap
Override the swap strategy:
```
HX-Reswap: outerHTML
```

### HX-Retarget
Override the swap target:
```
HX-Retarget: #error-container
```

### HX-Reselect
Override `hx-select`:
```
HX-Reselect: #content
```

**Important:** Response headers are NOT processed on 3xx redirect responses.

---

## Response Handling

### Default Behavior
| Status Code | Swap? | Error Event? |
|-------------|-------|--------------|
| 2xx | Yes | No |
| 204 No Content | No | No |
| 3xx | Browser redirect | N/A |
| 4xx | No | Yes |
| 5xx | No | Yes |

### Custom Configuration
```html
<meta name="htmx-config" content='{
  "responseHandling": [
    {"code": "204", "swap": false},
    {"code": "[23]..", "swap": true},
    {"code": "422", "swap": true, "error": true},
    {"code": "[45]..", "swap": false, "error": true}
  ]
}'>
```

### Common Pattern: Swap 422 Validation Errors
Many apps return `422 Unprocessable Entity` for validation failures. To swap the error HTML:

**Option 1:** Configure globally (above)

**Option 2:** Use `htmx:beforeSwap` event:
```javascript
document.body.addEventListener('htmx:beforeSwap', function(evt) {
    if (evt.detail.xhr.status === 422) {
        evt.detail.shouldSwap = true;
        evt.detail.isError = false;
    }
});
```

**Option 3:** Use the `response-targets` extension:
```html
<form hx-post="/submit" hx-target-422="#errors">
```

### DELETE Response Pattern
- Return `200` with empty body -> target is replaced with nothing (element removed)
- Return `204 No Content` -> NO swap occurs (element stays)

---

## Fragment Architecture

Organize templates with a clear separation between full pages and fragments:

```
templates/
  layouts/
    base.html          # Full page layout
  pages/
    index.html         # Full pages extending base
  partials/
    _item.html         # Reusable fragments (underscore prefix)
    _item_form.html    # Form fragments
    _item_list.html    # List fragments
    _error.html        # Error fragment
```

Fragments should be **reusable and isolated** — no dependencies on the surrounding page structure.

---

## Common Server Patterns

### Return HTML Fragments
```python
# FastAPI
@app.get("/contacts/{id}", response_class=HTMLResponse)
async def get_contact(request: Request, id: int):
    contact = get_contact_by_id(id)
    return templates.TemplateResponse("partials/_contact.html", {
        "request": request, "contact": contact
    })
```

### Out-of-Band Updates
Return extra HTML that updates other parts of the page:
```python
@app.post("/contacts", response_class=HTMLResponse)
async def create_contact(request: Request):
    form = await request.form()
    contact = create_new_contact(form)
    # Main response + OOB update for the contact list
    return HTMLResponse(f"""
        <form hx-post="/contacts">...</form>
        <tr id="contact-{contact.id}" hx-swap-oob="beforeend:#contacts-table">
            <td>{contact.name}</td>
        </tr>
    """)
```

### Trigger Events from Server
```python
from fastapi.responses import HTMLResponse

@app.post("/contacts", response_class=HTMLResponse)
async def create_contact(request: Request):
    form = await request.form()
    contact = create_new_contact(form)
    html = templates.TemplateResponse("partials/_contact_form.html", {"request": request})
    html.headers["HX-Trigger"] = json.dumps({
        "contactCreated": {"id": contact.id},
        "showMessage": "Contact created!"
    })
    return html
```

### Conditional Full Page vs Fragment
```python
@app.get("/page", response_class=HTMLResponse)
async def page(request: Request):
    if request.headers.get("HX-Request"):
        return templates.TemplateResponse("partials/_page_content.html", {"request": request})
    return templates.TemplateResponse("pages/page.html", {"request": request})
```

### Redirect After Form Submission
```python
@app.post("/login", response_class=HTMLResponse)
async def login(request: Request):
    form = await request.form()
    if authenticate(form):
        response = HTMLResponse("")
        response.headers["HX-Redirect"] = "/dashboard"
        return response
    return templates.TemplateResponse("partials/_login_error.html", {
        "request": request
    }, status_code=422)
```

---

## Framework Examples

### Python - Django
```python
from django.http import HttpResponse
from django.template.loader import render_to_string

def contact_list(request):
    contacts = Contact.objects.all()
    if request.headers.get('HX-Request'):
        html = render_to_string('contacts/_list.html', {'contacts': contacts})
        return HttpResponse(html)
    return render(request, 'contacts/index.html', {'contacts': contacts})

def delete_contact(request, pk):
    Contact.objects.filter(pk=pk).delete()
    return HttpResponse('')  # Empty 200 = remove element
```

### Python - Flask
```python
@app.route('/search', methods=['POST'])
def search():
    query = request.form.get('q', '')
    results = Contact.query.filter(Contact.name.ilike(f'%{query}%')).all()
    return render_template('_search_results.html', results=results)
```

### Java - Spring Boot
```java
@Controller
public class ContactController {
    @GetMapping("/contacts")
    public String list(Model model,
            @RequestHeader(value = "HX-Request", required = false) String htmx) {
        model.addAttribute("contacts", contactRepo.findAll());
        return htmx != null ? "contacts :: list" : "contacts";
    }

    @DeleteMapping("/contacts/{id}")
    public ResponseEntity<String> delete(@PathVariable Long id) {
        contactRepo.deleteById(id);
        return ResponseEntity.ok("");
    }
}
```

### Go
```go
func contactsHandler(w http.ResponseWriter, r *http.Request) {
    contacts := getContacts()
    if r.Header.Get("HX-Request") == "true" {
        tmpl.ExecuteTemplate(w, "contacts-list", contacts)
    } else {
        tmpl.ExecuteTemplate(w, "contacts-page", contacts)
    }
}
```

---

## Quirks

### GET on Non-Form Elements Excludes Form Values
```html
<!-- This button will NOT include the input's value -->
<form>
  <input name="query" value="test">
  <button hx-get="/search">Search</button>
</form>

<!-- Fix: include the form explicitly -->
<button hx-get="/search" hx-include="closest form">Search</button>
```

### 204 No Content = No Swap
A `204` response causes htmx to do nothing. For DELETE operations that should remove an element, return `200` with an empty body instead.

### Body Targeting Always Uses innerHTML
`hx-swap="outerHTML"` on a target of `<body>` is automatically converted to `innerHTML`. You cannot replace the `<body>` element itself.

### hx-boost Caveats
- Does not push URL for forms by default (only for anchors)
- Can cause issues with scripts that expect full page load
- Head tags need the `head-support` extension to be managed properly
- Some htmx team members advise against using it due to complexity

### Loading htmx Asynchronously
htmx expects to be loaded via a blocking `<script>` tag. Using `type="module"`, `defer`, or dynamic import can cause initialization issues where htmx misses elements that are already in the DOM.

### Attribute Inheritance Can Be Surprising
Many attributes inherit from parents, which can lead to unexpected behavior:
```html
<div hx-target="#output">           <!-- All children inherit this target -->
  <button hx-get="/a">Uses #output</button>
  <button hx-get="/b">Uses #output too -- maybe unintended?</button>
</div>
```

Disable globally with `htmx.config.disableInheritance = true`, then opt-in with `hx-inherit`.

### History Cache Conflicts with 3rd-Party JS
Libraries that modify the DOM (charts, rich text editors, etc.) may not restore properly from htmx's localStorage history cache. Solutions:
- Set `htmx.config.historyCacheSize = 0`
- Use `hx-history="false"` on affected pages
- Re-initialize libraries via `htmx:historyRestore` event
