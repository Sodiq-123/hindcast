# Design brief

Agreed with Sodiq on 17 Nov 2026. This brief guides the redesign. The current look (dark panels, badges, generic
dashboard) is a placeholder to replace.

## Order of work

1. Redesign, together: references → three directions → choose and mix → build. **Built for every page on 19 Nov 2026 and named Hindcast; the user's review is still open.**
2. Step 2 of personalisation: learning from what people do. **Built 19 Nov 2026: suggestions, “Worth a look” reminders, “What it’s learned”, practice rounds and the personal model (early read from 10 trades, full test from 30).**
3. Hosting.

## Personality: a calm, honest coach

The app tells people the truth about uncertainty: it says "no edge", flags copycat coins and counts fees. Its look
should feel like a sharp friend who knows markets and keeps you safe, not a trading floor.

- **Words first:** plain sentences lead, numbers support them.
- **Calm by default:** no flashing or ticker noise. Colour only where it carries meaning.
- **Honest about uncertainty:** show ranges ("it could land anywhere in here"), not confident arrows.
- **Warm and personal:** it greets people by name and remembers what they care about.
- **No character:** the app has one consistent voice, but no name, face or mascot.
- **Voice adapts to level:** friendly and explanatory for beginners, short and technical for experienced people. Each
  person can change it.
- **Name: Hindcast** (chosen 19 Nov 2026, replacing "Market Oracle", which promised certainty). A hindcast is a
  forecast made for the past to test the method, which is exactly how the app earns trust. Sign-in and the glossary
  explain the word for beginners.

## First screen

Combine these, from top to bottom:

1. **A personal briefing:** a few plain sentences about their things, including what changed since their last visit and
   any warnings. On a quiet day it shrinks to one line.
2. **One suggested next step,** for example "NVDA has no stop-loss in your journal" or "Learn what P/E means".
3. **Their watchlist.**

## Personalisation

- **Who decides:** the app suggests and the person decides. For example: "You check crypto most. Move it to the top?"
  The person accepts, declines or rearranges by hand. Every suggestion and the reason for it can be seen on About you.
- **Content:** what is shown, its order, the level of detail and the voice follow the profile (and later, behaviour).
- **Look:** each person picks:
  - light, dark or auto (follows their device)
  - an accent colour, from a small tested set
  
  The base design is otherwise the same for everyone.

## Constraints

- **Phone and computer equally:** every screen gets both layouts.
- **Motion is subtle:** gentle transitions and small touches like the eye opening, and nothing flashy. Respect
  "reduce motion".
- **Up and down:** green and red, always paired with ▲ and ▼ so colour-blind people can read them.
- **Accessible:** readable contrast in both themes, and usable with a keyboard.
- **Personalisation never changes a number:** forecasts, test results and warnings are the same for everyone.

## Next

1. Gather references from finance and non-finance products.
2. Build three distinct directions as clickable mockups of the first screen, using real data.
3. Choose and mix them together.

## Decision (2026-11-17)

Chosen direction: **B · The Range**, plus A's **"One thing to do today"** card.

- Shell: B's left sidebar on computer, bottom tab bar on phone. Fonts: Space Grotesk (headings), Inter (text), JetBrains Mono (numbers and tags). Default accent teal #0E7C86; people can pick their own accent and light/dark/auto.
- Home order: greeting → "Since you last looked" (tagged lines: EARNINGS, RISK, MOVES, SIGNALS) → "One thing to do today" (big numeral, one action, *Open* + *Not today*) → shared-axis range chart with Day/Week/Month/Year → token safety meter.
- "Not today" collapses the card to a one-line note with Undo; the decline is a signal for step 2 (behaviour-based learning).
- Mockups: https://claude.ai/artifact/YZkYM2Q4tmD1bwgKXbS6Wz (top two boards).
