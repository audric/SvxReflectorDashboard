Rails.application.routes.draw do
  root 'dashboard#index'
  get 'stats', to: 'dashboard#stats', as: :stats
  get 'map',   to: 'dashboard#map',   as: :map
  get 'tg',    to: 'dashboard#tg',    as: :tg

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
    resource :settings, only: %i[edit update]
  end

  # Reveal health status on /up that returns 200 if the app boots with no exceptions, otherwise 500.
  # Can be used by load balancers and uptime monitors to verify that the app is live.
  get "up" => "rails/health#show", as: :rails_health_check
end
