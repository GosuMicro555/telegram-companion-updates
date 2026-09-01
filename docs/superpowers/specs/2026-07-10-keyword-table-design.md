# Compact Keyword Table Design

## Goal

Make the keyword screen practical for lists up to 1000 entries without losing the existing search, bulk import, shared reply, direct-message setting, or database persistence.

## Scrolling

- The application shell remains viewport-height.
- The right content area owns vertical page scrolling.
- The sidebar remains stable while the keyword controls and table scroll naturally as one page.
- The keyword panel no longer creates a competing nested scroll region.

## Keyword Table

- Use three columns: `Keyword`, `Direct messages`, and `Actions`.
- Keep the full Russian label `Личные сообщения`; horizontal space does not need to be minimized.
- Use compact rows around 40 pixels high.
- Do not repeat the shared reply under every keyword; it remains editable once above the table.
- Keep search and counters above the rows.

## Editing And Deletion

- The edit icon changes the keyword cell into an inline text input.
- A check icon saves the normalized, non-empty, unique value; a cancel icon restores the old value.
- The delete icon changes into an inline confirmation state to prevent accidental deletion.
- Every edit, delete, and direct-message toggle flows through the existing debounced `SaveKeywordSettings` persistence.

## Running Animation

- When automation is running, the visible `STOP` button receives a restrained moving highlight across its existing red/orange background.
- The animation does not change button dimensions or text.
- The animation is disabled under `prefers-reduced-motion: reduce`.

## Testing

- Unit-test keyword rename validation, duplicate prevention, and deletion.
- Run the existing frontend and Go test suites.
- Build the Wails desktop binary.
- Visually verify page scrolling, compact rows, inline edit/delete, persistence after reload, and the running-button animation in Ubuntu.
