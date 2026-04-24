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
      file = params[:file]
      return render(json: { error: "no file" }, status: :unprocessable_entity) if file.blank?

      blob = ActiveStorage::Blob.create_and_upload!(
        io: file.to_io,
        filename: file.original_filename,
        content_type: file.content_type
      )
      render json: { url: url_for(blob) }
    rescue => e
      Rails.logger.error("Info image upload failed: #{e.class}: #{e.message}")
      render json: { error: e.message }, status: :internal_server_error
    end
  end
end
