Rails.application.routes.draw do
  root 'dashboard#index'
  get 'stats', to: 'dashboard#stats', as: :stats
  get 'map',   to: 'dashboard#map',   as: :map
  get 'tg',    to: 'dashboard#tg',    as: :tg
  get 'radio_programming', to: 'dashboard#radio', as: :radio
  get 'events', to: 'dashboard#events', as: :events
  get 'trunks', to: 'dashboard#trunks', as: :trunks

  get    "login",    to: "sessions#new"
  post   "login",    to: "sessions#create"
  delete "logout",   to: "sessions#destroy"
  get    "register", to: "registrations#new"
  post   "register", to: "registrations#create"

  namespace :admin do
    resources :users do
      member do
        patch :approve
      end
    end
    resources :tgs, except: :show
    resource :settings, only: :update
    resource :reflector, only: %i[edit update], controller: "reflector" do
      get :backups, on: :collection
      get :pending_csrs, on: :collection
      get :inspect_csr, on: :collection
      get :certificates, on: :collection
      get :export_ca_bundle, on: :collection
      post :sign_csr, on: :collection
      post :reject_csr, on: :collection
      post :reset_pki, on: :collection
      post :block_node, on: :collection
      post :revoke_cert, on: :collection
    end
    resources :external_reflectors
    resources :bridges, controller: "bridge" do
      collection do
        get :xlx_hosts
      end
      member do
        patch :toggle
        get :backups
        get :logs
      end
    end
    resource :system_info, only: :show, controller: "system_info"
    resource :debug, only: :show, controller: "debug"
    resource :mqtt, only: :show, controller: "mqtt"
    resource :logs, only: :show, controller: "logs" do
      get :fetch, on: :collection
    end
  end

  # Reveal health status on /up that returns 200 if the app boots with no exceptions, otherwise 500.
  # Can be used by load balancers and uptime monitors to verify that the app is live.
  get "up" => "rails/health#show", as: :rails_health_check
end
