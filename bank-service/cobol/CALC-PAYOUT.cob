       IDENTIFICATION DIVISION.
       PROGRAM-ID. CALC-PAYOUT.
       AUTHOR. SWARM-BLACKJACK.
      *----------------------------------------------------------------*
      * Calculates payout amount given bet and result.
      *
      * Blackjack payout rules:
      *   WIN  - player receives 2x the bet (profit + original stake)
      *   PUSH - player receives 1x the bet (original stake returned)
      *   LOSS - player receives nothing
      *
      * Input  (environment variables):
      *   BET_CENTS    - original bet amount in cents (integer)
      *   RESULT       - WIN, LOSS, or PUSH
      *
      * Output (stdout, key=value lines):
      *   RETURNED_CENTS  - amount to credit back to player
      *   PAYOUT_TYPE     - payout_win, payout_loss, or payout_push
      *
      * Exit code: 0 = success, 1 = error
      *----------------------------------------------------------------*

       ENVIRONMENT DIVISION.

       DATA DIVISION.
       WORKING-STORAGE SECTION.
       01 WS-BET-CENTS        PIC 9(15)  VALUE ZERO.
       01 WS-RESULT           PIC X(9)   VALUE SPACES.
       01 WS-RETURNED-CENTS   PIC 9(15)  VALUE ZERO.
       01 WS-PAYOUT-TYPE      PIC X(14)  VALUE SPACES.
       01 WS-RESULT-TRIMMED   PIC X(9)   VALUE SPACES.

       PROCEDURE DIVISION.
       MAIN-PARA.
           ACCEPT WS-BET-CENTS FROM ENVIRONMENT "BET_CENTS"
           ACCEPT WS-RESULT    FROM ENVIRONMENT "RESULT"

           MOVE FUNCTION UPPER-CASE(
               FUNCTION TRIM(WS-RESULT LEADING))
               TO WS-RESULT-TRIMMED

           EVALUATE WS-RESULT-TRIMMED
               WHEN "BLACKJACK"
      *            Natural blackjack: 3:2 payout (stake + 1.5x profit)
      *            Profit rounded DOWN to nearest dollar — house keeps half-chip.
      *            Step 1: profit = floor((bet * 3) / 2) in cents
      *            Step 2: round down to nearest 100 cents (whole dollar)
      *            Step 3: return stake + rounded profit
                   COMPUTE WS-RETURNED-CENTS = (WS-BET-CENTS * 3) / 2
                   COMPUTE WS-RETURNED-CENTS =
                       (WS-RETURNED-CENTS / 100) * 100
                   COMPUTE WS-RETURNED-CENTS =
                       WS-BET-CENTS + WS-RETURNED-CENTS
                   MOVE "payout_win"  TO WS-PAYOUT-TYPE

               WHEN "WIN"
      *            Win: return 2x the bet (profit plus original stake)
                   COMPUTE WS-RETURNED-CENTS = WS-BET-CENTS * 2
                   MOVE "payout_win"  TO WS-PAYOUT-TYPE

               WHEN "PUSH"
      *            Push: return the original stake only
                   MOVE WS-BET-CENTS  TO WS-RETURNED-CENTS
                   MOVE "payout_push" TO WS-PAYOUT-TYPE

               WHEN "LOSS"
      *            Loss: house keeps everything
                   MOVE ZERO          TO WS-RETURNED-CENTS
                   MOVE "payout_loss" TO WS-PAYOUT-TYPE

               WHEN OTHER
                   DISPLAY "ERROR=unknown result: " WS-RESULT
                   STOP RUN RETURNING 1
           END-EVALUATE

           DISPLAY "RETURNED_CENTS=" WS-RETURNED-CENTS
           DISPLAY "PAYOUT_TYPE="    WS-PAYOUT-TYPE
           STOP RUN.
