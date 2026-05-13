module Admin
  class InfoPagesController < ApplicationController
    layout false
    before_action :require_admin
    before_action :set_page, only: %i[edit update destroy toggle_published move_up move_down]

    def index
      @pages = InfoPage.ordered
    end

    def new
      @page = InfoPage.new
    end

    def create
      @page = InfoPage.new(page_params)
      if @page.save
        redirect_to edit_admin_info_page_path(@page), notice: "Page created."
      else
        render :new, status: :unprocessable_entity
      end
    end

    def edit; end

    def update
      if @page.update(page_params)
        redirect_to edit_admin_info_page_path(@page), notice: "Page saved."
      else
        render :edit, status: :unprocessable_entity
      end
    end

    def destroy
      @page.destroy
      redirect_to admin_info_pages_path, notice: "Page deleted."
    end

    def toggle_published
      @page.update!(published: !@page.published)
      redirect_to admin_info_pages_path, notice: "#{@page.title} #{@page.published ? 'published' : 'unpublished'}."
    end

    def move_up
      swap_with_neighbor(:above)
      redirect_to admin_info_pages_path
    end

    def move_down
      swap_with_neighbor(:below)
      redirect_to admin_info_pages_path
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

    private

    def set_page
      @page = InfoPage.find_by!(slug: params[:id])
    end

    def page_params
      params.require(:info_page).permit(:slug, :title, :body, :published)
    end

    def swap_with_neighbor(direction)
      neighbor =
        if direction == :above
          InfoPage.where("position < ?", @page.position).order(position: :desc).first
        else
          InfoPage.where("position > ?", @page.position).order(position: :asc).first
        end
      return unless neighbor

      InfoPage.transaction do
        a, b = @page.position, neighbor.position
        @page.update_columns(position: b)
        neighbor.update_columns(position: a)
      end
    end
  end
end
