module Admin
  class InfoController < ApplicationController
    layout false
    before_action :require_admin

    def edit
      @body = Setting.get("system_description", "")
    end

    def update
      Setting.set("system_description", params.fetch(:body, ""))
      redirect_to edit_admin_info_path, notice: "Info page saved."
    end

    def upload_image
      head :not_implemented
    end
  end
end
