# Peak - An Acme-inspired TUI Text Editor

<img width="2423" height="1514" alt="Peak Screenshot" src="https://github.com/user-attachments/assets/914ec212-55ab-48ad-bbda-53e85e70912e" />

## What
*   This is a replicate of Plan 9 Acme in a TUI environment. It doesn't aim to be a bit-by-bit clone but strives to be somewhat usable and potentially better than other simple terminal text editors.
*   This project was developed with the assistance of Gemini CLI. However, this does not mean it is vibe coded. I stop and review its code at almost every change, and roll back or modify any changes that don't meet my preferences (As a result, at least 2/3 of changes was reset). You can confirm the code quality.
*   I hope Rob Pike won't see this. If he does, he can consult Gemini directly, as he, Go, and Gemini all belong to the same company.
*   Some people may ask me why I did this, because when I first made this project, I was just looking for something to do in a few days of boring classes, and an idea I had many years ago popped into my head.
*   **Warning: this is a toy project that may not become mature.**

## How

```bash
CGO_ENABLED=0 go build .
./peak
```

## Usage

Simimar to [Acme's](https://9p.io/wiki/plan9/Using_acme/index.html) but sometimes may be different.
