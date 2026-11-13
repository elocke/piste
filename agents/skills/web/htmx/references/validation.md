# Validation Pattern

## Server-Side Validation Rules

On validation error:

1. Return the **same form fragment** (not a redirect)
2. Include inline **error messages** next to fields
3. **Preserve user input** in form fields
4. Use HTTP **422 Unprocessable Entity** status code

## Example (FastAPI/Jinja2)

```python
@app.post("/items", response_class=HTMLResponse)
async def create_item(request: Request):
    form = await request.form()
    errors = validate_item(form)
    if errors:
        return templates.TemplateResponse(
            "_item_form.html",
            {"request": request, "errors": errors, "values": dict(form)},
            status_code=422,
        )
    item = save_item(form)
    return templates.TemplateResponse("_item_row.html", {"request": request, "item": item})
```

## Client-Side Handling

To swap 422 responses into the DOM, either configure globally:
```html
<meta name="htmx-config" content='{"responseHandling": [{"code": "422", "swap": true}]}'>
```

Or use the `response-targets` extension:
```html
<form hx-post="/items" hx-target="#result" hx-target-422="#form-container">
```

## Key Rules

- Do NOT redirect on validation failure
- Do NOT return JSON errors for htmx requests
- Validate on the server always, even if client-side validation exists
- Return focused, minimal error fragments
