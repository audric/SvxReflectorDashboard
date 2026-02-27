module Admin
  class SettingsController < ApplicationController
    layout false
    before_action :require_admin

    KEYS = %w[reflector_status_url brand_name poll_interval].freeze

    def edit
      @settings = KEYS.index_with { |key| Setting.get(key, defaults[key]) }
    end

    def update
      KEYS.each do |key|
        Setting.set(key, params[:settings][key].presence)
      end
      redirect_to edit_admin_settings_path, notice: "Settings saved"
    end

    private

    def defaults
      {
        "reflector_status_url" => ENV.fetch("REFLECTOR_STATUS_URL", ""),
        "brand_name" => ENV.fetch("BRAND_NAME", ""),
        "poll_interval" => "4"
      }
    end
  end
end
