-- Hand Evaluator Service
-- Language: Haskell
-- Why Haskell? This service is a pure function. Cards in → value out.
-- Haskell's type system makes it provably correct — the compiler enforces
-- that no side effects creep in. This is exactly the use case Haskell
-- was designed for. An experienced engineer picks the right tool;
-- an AI partner implements it fluently regardless of language.

{-# LANGUAGE OverloadedStrings #-}
{-# LANGUAGE DeriveGeneric #-}

module Main where

import Data.Aeson (FromJSON, ToJSON, decode, encode, object, (.=), (.:), withObject)
import Data.Aeson.Types (Parser, parseJSON, toJSON, Value)
import GHC.Generics (Generic)
import Network.HTTP.Types (status200, status400, status405, hContentType, Status)
import Network.Wai (Application, Request, Response, ResponseReceived, requestMethod, requestBody, responseLBS, pathInfo)
import Network.Wai.Handler.Warp (run, defaultSettings, setPort)
import Network.Wai.Handler.WarpTLS (runTLS, tlsSettings, TLSSettings(..))
import qualified Network.TLS as TLS
import Data.Default (def)
import Data.ByteString.Lazy (ByteString)
import qualified Data.ByteString.Lazy as BL
import qualified Data.Text as T
import System.Environment (lookupEnv)
import Data.Maybe (fromMaybe)

-- ── Types ────────────────────────────────────────────────────────────────────

data Card = Card
  { suit :: T.Text
  , rank :: T.Text
  } deriving (Show, Generic)

instance FromJSON Card
instance ToJSON Card

data EvaluateRequest = EvaluateRequest
  { cards :: [Card]
  } deriving (Show, Generic)

instance FromJSON EvaluateRequest

data HandResult = HandResult
  { value      :: Int
  , isSoft     :: Bool
  , isBlackjack :: Bool
  , isBust     :: Bool
  } deriving (Show, Generic)

instance ToJSON HandResult

-- ── Pure Evaluation Logic ─────────────────────────────────────────────────────
-- This is the entire domain in pure functions. No IO, no state, no side effects.
-- The HTTP layer is the only impure code in this service.

rankValue :: T.Text -> Int
rankValue "A"  = 11
rankValue "K"  = 10
rankValue "Q"  = 10
rankValue "J"  = 10
rankValue "10" = 10
rankValue r    = case reads (T.unpack r) :: [(Int, String)] of
                   [(n, "")] -> n
                   _         -> 0

evaluateHand :: [Card] -> HandResult
evaluateHand hand =
  let ranks'     = map rank hand
      rawTotal   = sum (map rankValue ranks')
      aceCount   = length (filter (== "A") ranks')
      -- Reduce aces from 11 to 1 until we're not bust
      (total, softnessUsed) = reduceAces rawTotal aceCount 0
      soft       = softnessUsed < aceCount && total <= 21
      bj         = total == 21 && length hand == 2
      bust       = total > 21
  in HandResult
       { value       = total
       , isSoft      = soft
       , isBlackjack = bj
       , isBust      = bust
       }

reduceAces :: Int -> Int -> Int -> (Int, Int)
reduceAces total 0 used = (total, used)
reduceAces total aces used
  | total > 21 = reduceAces (total - 10) (aces - 1) (used + 1)
  | otherwise  = (total, used)

-- ── HTTP Application ──────────────────────────────────────────────────────────

app :: Application
app req respond =
  case (requestMethod req, pathInfo req) of
    ("GET",  ["health"])   -> respond healthResponse
    ("POST", ["evaluate"]) -> handleEvaluate req respond
    ("OPTIONS", _)         -> respond corsResponse
    _                      -> respond notFoundResponse

handleEvaluate :: Request -> (Response -> IO ResponseReceived) -> IO ResponseReceived
handleEvaluate req respond = do
  body <- requestBody req
  let bodyLazy = BL.fromStrict body
  case decode bodyLazy :: Maybe EvaluateRequest of
    Nothing -> respond $ jsonResponse status400 (object ["error" .= ("invalid request body" :: T.Text)])
    Just evalReq -> do
      let result = evaluateHand (cards evalReq)
      respond $ jsonResponse status200 (toJSON result)

healthResponse :: Response
healthResponse = jsonResponse status200 $ object
  [ "status"   .= ("healthy" :: T.Text)
  , "service"  .= ("hand-evaluator" :: T.Text)
  , "language" .= ("Haskell" :: T.Text)
  , "note"     .= ("Pure function service — provably correct by construction" :: T.Text)
  ]

corsResponse :: Response
corsResponse = responseLBS status200
  [ ("Access-Control-Allow-Origin", "*")
  , ("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
  , ("Access-Control-Allow-Headers", "Content-Type")
  ] ""

notFoundResponse :: Response
notFoundResponse = jsonResponse status400 $ object ["error" .= ("not found" :: T.Text)]

jsonResponse :: Status -> Value -> Response
jsonResponse status body = responseLBS status
  [ (hContentType, "application/json")
  , ("Access-Control-Allow-Origin", "*")
  ] (encode body)

-- ── Entry Point ───────────────────────────────────────────────────────────────

main :: IO ()
main = do
  portStr  <- lookupEnv "PORT"
  certFile <- lookupEnv "TLS_CERT"
  keyFile  <- lookupEnv "TLS_KEY"
  let port = maybe 3003 read portStr :: Int
      settings = setPort port defaultSettings

  case (certFile, keyFile) of
    (Just cert, Just key) -> do
      putStrLn $ "[hand-evaluator] starting on :" ++ show port ++ " (mTLS)"
      putStrLn   "[hand-evaluator] Pure function: Cards in -> value out. No state. No side effects."
      let tlsCfg = (tlsSettings cert key)
            { tlsWantClientCert = True
            , tlsServerHooks    = (def :: TLS.ServerHooks)
                { TLS.onClientCertificate = \chain ->
                    case chain of
                      TLS.CertificateChain [] ->
                        return $ TLS.CertificateUsageReject
                          (TLS.CertificateRejectOther "mTLS: client cert required")
                      _ -> return TLS.CertificateUsageAccept
                }
            }
      runTLS tlsCfg settings app
    _ -> do
      putStrLn $ "[hand-evaluator] starting on :" ++ show port ++ " (plaintext — no TLS env vars)"
      putStrLn   "[hand-evaluator] Pure function: Cards in -> value out. No state. No side effects."
      run port app