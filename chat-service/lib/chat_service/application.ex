defmodule ChatService.Application do
  @moduledoc """
  Chat Service — Elixir / OTP

  Why Elixir? This is literally what it was designed for.
  WhatsApp served 900M users with 50 engineers. OTP supervision trees
  mean a crashed process restarts in isolation — it NEVER takes down the game.
  The actor model makes concurrent messaging trivially correct.

  Security posture:
  - E2E encrypted messages (Signal protocol — TODO: implement)
  - Public table chat + targeted private messages
  - WebSocket connections (bidirectional — genuinely needed for chat)
  - Rate limiting per-player via OTP process state
  """
  use Application

  def start(_type, _args) do
    port      = String.to_integer(System.get_env("PORT")     || "3007")
    tls_cert  = System.get_env("TLS_CERT")
    tls_key   = System.get_env("TLS_KEY")
    tls_ca    = System.get_env("TLS_CA")

    cowboy_child =
      if tls_cert && tls_key && tls_ca do
        {Plug.Cowboy,
          scheme: :https,
          plug: ChatService.Router,
          options: [
            port: port,
            certfile: tls_cert,
            keyfile:  tls_key,
            cacertfile: tls_ca,
            verify: :verify_peer,
            fail_if_no_peer_cert: true
          ]}
      else
        {Plug.Cowboy, scheme: :http, plug: ChatService.Router, options: [port: port]}
      end

    children = [cowboy_child, ChatService.TableRegistry]
    opts = [strategy: :one_for_one, name: ChatService.Supervisor]

    mode = if tls_cert, do: "(mTLS)", else: "(plaintext — no TLS env vars)"
    IO.puts("💬 Chat Service (Elixir/OTP) starting on :#{port} #{mode}")
    IO.puts("   Actor model: each table is a supervised process.")
    IO.puts("   Crash isolation: a dead table never kills the game.")

    Supervisor.start_link(children, opts)
  end
end
