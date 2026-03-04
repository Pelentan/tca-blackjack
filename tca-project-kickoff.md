# Starting a TCA Project

**Last Updated:** 2026-03-04  
**Audience:** Engineers beginning their first TCA project  
**Tone:** This is not a checklist. It's a thinking guide.

---

## Before You Touch a Keyboard

You have an idea for an application. Maybe it's fully formed in your head, maybe it's still fuzzy around the edges. Either is fine. The first thing TCA asks of you is something that feels counterintuitive if you've spent your career diving straight into code: sit with it for a while before you write anything.

Not because planning is inherently virtuous. Because the decisions you make in the next hour determine whether you build something you can maintain, evolve, and rewrite three years from now — or something you're apologizing for in six months.

Talk it through with your AI partner. Not "here's what I need, build it" — that's not a conversation, that's a command. Explain what you're trying to accomplish. Describe how you imagine someone using it. Talk about what it needs to get right versus what's nice to have. Let the AI push back, ask questions, and surface things you haven't considered. This is the cheapest problem-solving you'll do on the project. Everything is still malleable.

The output of this conversation is not a document or a spec. It's clarity. You should be able to describe your application in plain language, including what it's not trying to do, before you move to the next step.

---

## The Big Mental Shift: Finding the Jobs

This is the part that changes how you think about software, and it's the part that can't be rushed. Everything else in TCA follows from it naturally. If you get this wrong, you'll feel it for the rest of the project. If you get it right, the rest of the project will feel like it's building itself.

The shift is this: **stop thinking about your application as a thing, and start thinking about it as a set of things that happen.**

When most engineers approach a new application, they think in terms of features, screens, data models, or technology stacks. All of that is real and important — but it's the wrong starting point for decomposition. Features are about what users experience. Jobs are about what the system does. Those are related but not the same thing.

### How to Find the Seams

Start by describing your application as a series of actions. Not user stories, not use cases — just actions. What does the system actually *do*?

Take a blackjack game as an example. Most engineers would think: "It's a card game. I need cards, players, a dealer, a betting system, and a chat feature." That's a feature list. It tells you what the user sees.

The action list looks different: *shuffle a deck, deal cards, evaluate a hand, apply rules, manage a player's chips, authenticate a user, send a notification, record what happened*. Each one of those is a candidate Job. Not because you've decided to make them separate yet — but because each one does something distinct and complete.

Now ask yourself the separating questions:

**Does this need to know what anything else is doing?**  
A hand evaluator takes cards and returns a value. It doesn't need to know there's a betting system. It doesn't need to know there's a player. It doesn't need to know the game is blackjack. If a piece of logic needs zero knowledge of everything around it to do its work, that's a strong signal it belongs in its own Job.

**If this broke, what would stop working?**  
If the answer is "everything," it's probably doing too much or sitting in the wrong place. If the answer is "just this one thing," you've found a good boundary. The deck service going down should stop cards from being dealt. It should not stop a player from checking their chip balance.

**Would different domains solve this differently?**  
Financial arithmetic wants fixed-point decimal and no rounding ambiguity. Real-time messaging wants concurrency and fault isolation. Pure computation wants determinism and provable correctness. When the problem domain suggests a different approach — or even a different language — that's the seam telling you where the boundary is.

**Does this change at its own pace?**  
A card shuffling algorithm doesn't change when authentication requirements change. A billing system doesn't change when a UI gets redesigned. Things that change together belong together. Things that change independently belong apart.

### What a Good Job Looks Like

A well-defined Job has three properties:

It does *one thing*. Not one feature — one *function*. "Manage the game" is not one thing. "Evaluate whether this hand wins or loses given these cards and this dealer hand" is one thing.

It knows nothing it doesn't need to know. The hand evaluator knows what a card is. It doesn't know what a player is, what a bet is, or what happens after it returns a value. If you're designing a Job and you find it needs to import concepts from another domain, the boundary is in the wrong place.

You could describe it to a non-engineer in one sentence without simplifying. If your one-sentence description needs a "and also" in it, the Job is doing two things.

### Where People Get Stuck

The most common mistake at this stage is granularity anxiety. People either make Jobs too large ("the game service handles all game logic") or too small ("a Job just to validate that a card rank is between 1 and 13"). Neither is inherently wrong, but both are signals to pause.

Too large: if you can't explain what the Job does without listing multiple distinct responsibilities, it needs to be split. The test is whether you could rewrite it in an afternoon. A Job that does "all game logic" cannot be rewritten in an afternoon.

Too small: if a Job has no meaningful state, no interesting logic, and would change every time its one caller changes, it might not be a Job — it might just be a function that belongs inside a larger Job.

The right size is the one where the boundary feels clean and the Job feels complete. You'll know it when you find it. It feels like a satisfying definition, not a compromise.

---

## What Decomposition Leads To

Here's the thing nobody tells you when they explain microservices: once you've found the real boundaries, everything else becomes obvious. Not easy, but obvious. The architecture starts making its own decisions.

Once you have Jobs with real boundaries, you immediately notice that Jobs need to talk to each other — and that they can only know what the other Job explicitly offers. You need a defined interface. You need to agree on what goes in and what comes out before either side can be built. You need, without quite realizing it yet, a Contract.

And once you've written the Contract — once you've said "this Job accepts exactly these inputs and returns exactly these outputs, and that's all anyone ever needs to know" — you've also answered the language question. The Contract doesn't care what's inside the container. Suddenly you're free to pick the right language for the right Job instead of the one everyone on the team knows.

And once language is a free variable, the three-year lifecycle stops being a scary idea and starts being a reasonable one. If a Job can be rewritten in a different language without changing a single other thing in the system, then a rewrite is just a better version of the same Job. The architecture absorbs it without drama.

None of that is imposed by TCA. It follows from getting decomposition right. TCA is the formalization of the thing that was always going to happen if you thought about the problem clearly enough.

---

## Talking It Through With Your AI Partner

Once you have a mental model of your Jobs — not a final list, just a working one — go back to your AI partner and describe them. Not as a specification. As a conversation.

"I'm thinking the authentication piece should be its own Job. It needs to handle registration, login, and session management. But I'm not sure whether the session validation that happens on every request should be inside that Job or at the gateway."

That's the kind of question an AI partner can engage with substantively. It has seen this pattern resolved many different ways. It can tell you what the tradeoffs are. It can push back if your proposed boundary creates a dependency that will cause problems later. It can ask the questions you haven't thought to ask.

This is the back-and-forth that defines what TCA projects actually look and feel like. Not "generate me an application." A genuine design conversation where you bring judgment and experience and the AI brings breadth and precision.

By the end of this conversation, you should have:
- A named list of Jobs with one-sentence descriptions
- A rough sense of which language suits each Job and why
- Agreement on which Jobs will be stubbed initially
- A shared understanding of what the zero-trust posture requires from the start

You're not committing to anything permanent yet. Jobs can be split, merged, or renamed. But you need enough definition to write the first Contracts.

---

## Before Code: Commit Zero

This comes before implementation and before contracts. It is not glamorous but it is non-negotiable, and the reason it's non-negotiable is that it's the kind of thing you only regret skipping once.

Your repository gets three things before anything else:

**`.gitignore`** — Generated for your full polyglot stack. Not a generic template. The actual languages and frameworks you've agreed on, covering compiled artifacts, dependency directories, environment files, generated certificates, IDE cruft, and any secret material paths you've already defined. If a file could ever contain a secret, its path is in `.gitignore` before that file is created.

**`.github/workflows/security.yml`** — CodeQL scanning configured for every language in the stack. Security scanning from commit zero means every commit is scanned. Security scanning added three weeks in means everything before that wasn't.

**`README.md`** — What this is, what the Jobs are, what language each uses and why, how to run it from a clean clone. One paragraph on the AI-augmented development methodology. This is also the first test of whether your Job decomposition is clear: if you can't describe each Job in a sentence in the README, the decomposition needs more work.

Ask your AI partner to generate all three based on your agreed stack and Job list. It can produce all of them accurately in one pass. Do not write them manually.

---

## Now: The Contracts

With commit zero done and Jobs defined, you write Contracts. Every Job gets one. The AI writes them.

The sequence for each Job:

1. Tell the AI what the Job needs to receive and what it needs to return. Be specific about types. "A list of cards" is not specific. "An array of card objects, each with a `suit` field constrained to hearts/diamonds/clubs/spades and a `rank` field constrained to A/2-10/J/Q/K" is specific.

2. Work through the error cases. What does the Job return if the input is invalid? What if a dependency is unreachable? Every response the Job can produce needs to be in the Contract.

3. Have the AI write the Contract. Review it. The Contract is the most important document you'll produce — take the time to read it and understand what it says.

4. Ask yourself: does this Contract expose anything about the implementation? If you can infer what's inside the Job from the Contract shape, something is leaking. A well-designed Contract describes the surface and implies nothing about the interior.

Contracts for all Jobs are written before implementation of any Job begins. Not "write the contract for Job A, implement Job A, write the contract for Job B" — all contracts first. This forces you to think about the whole system before you're committed to any part of it, and it reveals integration problems at the cheapest possible moment.

---

## Then: Implementation

With contracts written and reviewed, you implement. The AI implements each Job against its contract. You watch the build, read the output, test the running system, and make architectural decisions when the path forks.

This is where the work-methods document takes over. The decomposition thinking, the contract review, the project definition — those were yours. The implementation is the partnership in motion.

---

## The Through-Line

What makes TCA feel different from other architectural approaches isn't the individual pieces — contracts, containers, defined interfaces, independent deployability. Those ideas have been around for decades. What makes it different is that the pieces connect.

Decomposition is not a preliminary step before the real work. It *is* the real work. When you find the right boundaries, Contracts become obvious. When Contracts are real, polyglot becomes practical. When polyglot is practical, the three-year lifecycle becomes feasible. When rewrites are feasible, technical debt becomes a choice instead of an inevitability.

The whole architecture is downstream of one decision: being rigorous about where one Job ends and another begins.

Everything else follows.
