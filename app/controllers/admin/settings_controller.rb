module Admin
  class SettingsController < ApplicationController
    layout false
    before_action :require_admin

    KEYS = %w[reflector_status_url brand_name reflector_ext_host poll_interval].freeze

    def update
      KEYS.each do |key|
        Setting.set(key, params[:settings][key].presence)
      end
      redirect_to admin_system_info_path(tab: "settings"), notice: "Settings saved"
    end

    private

    def defaults
      {
        "reflector_status_url" => ENV.fetch("REFLECTOR_STATUS_URL", ""),
        "brand_name" => ENV.fetch("BRAND_NAME", ""),
        "reflector_ext_host" => ENV.fetch("REFLECTOR_EXT_HOST", ""),
        "poll_interval" => "1"
      }
    end
  end
end
