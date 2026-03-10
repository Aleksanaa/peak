# Acme-like CLI Text Editor (Go) - Project Notes

## Vision
A terminal-based text editor inspired by Plan 9's Acme. It emphasizes a minimalist UI, tiling windows, and "everything is text" philosophy where commands can be executed by clicking or selecting text.

## Core Features (Acme Parity)
1.  **Tiling Windows:** Vertical columns and horizontal windows within columns.
2.  **Tag Lines:** Every window and column has a "tag line" for commands and status.
3.  **Mouse Integration:** (Where terminal supports it)
    -   Left-click: Select/Set cursor.
    -   Middle-click: Execute text (commands).
    -   Right-click: Search/Plumb text.
4.  **Chording:** Support for mouse button chords if possible.
5.  **Filesystem Interface:** (Optional/Advanced) Exposing editor state via a 9P-like interface.
6.  **External Commands:** Easy execution of shell commands with output redirection back into the editor.

## Technical Stack
-   **Language:** Go
-   **TUI Library:** `tcell` (low-level, robust mouse support) or `bubbletea` (Elm architecture). Given Acme's dynamic layout, `tcell` or a custom layer on top might be more flexible.
-   **Data Structures:** Gap buffer or Piece table for efficient text editing.

## Development Phases

### Phase 1: Foundation
-   [ ] Setup basic `tcell` loop.
-   [ ] Implement a basic `Window` and `Column` layout system.
-   [ ] Basic text rendering and scrolling.

### Phase 2: Input & Editing
-   [ ] Cursor movement (Keyboard).
-   [ ] Basic text insertion and deletion.
-   [ ] Mouse support: Clicking to position cursor.

### Phase 3: Acme Mechanics
-   [ ] Tag lines implementation.
-   [ ] Execution logic (Middle-click/Command parsing).
-   [ ] Search logic (Right-click).

### Phase 4: Refinement
-   [ ] File I/O (Load/Save).
-   [ ] Shell command integration (`|`, `>`, `<`).
-   [ ] Multiple columns.

## Architecture Ideas
-   **`View` interface:** Anything that can be drawn (Tag, Body).
-   **`Text` buffer:** Shared or individual buffers for tag and body.
-   **`Layout` manager:** Handles the tiling math.
