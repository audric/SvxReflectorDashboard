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

    def blobs
      pages = InfoPage.pluck(:slug, :title, :body)
      blobs = ActiveStorage::Blob.order(created_at: :desc).to_a
      @references = blobs.each_with_object({}) do |b, h|
        sig = b.signed_id
        h[b.id] = pages.each_with_object([]) do |(slug, title, body), acc|
          acc << [slug, title] if body.to_s.include?(sig)
        end
      end
      @used_blobs   = blobs.select { |b| @references[b.id].any? }
      @orphan_blobs = blobs.reject { |b| @references[b.id].any? }
      @orphan_bytes = @orphan_blobs.sum(&:byte_size)
    end

    def purge_blob
      blob = ActiveStorage::Blob.find(params[:blob_id])
      if blob_referenced?(blob)
        redirect_to blobs_admin_info_pages_path, alert: "Blob ##{blob.id} (#{blob.filename}) is still referenced by a page — not deleted."
      else
        name = blob.filename.to_s
        size = blob.byte_size
        blob.purge
        redirect_to blobs_admin_info_pages_path, notice: "Deleted #{name} (#{format_bytes(size)})."
      end
    end

    def purge_orphan_blobs
      orphans = ActiveStorage::Blob.all.reject { |b| blob_referenced?(b) }
      count = orphans.size
      total = orphans.sum(&:byte_size)
      orphans.each(&:purge)
      redirect_to blobs_admin_info_pages_path, notice: "Deleted #{count} orphan blob(s) — freed #{format_bytes(total)}."
    end

    private

    def blob_referenced?(blob)
      sig = blob.signed_id
      InfoPage.where("body LIKE ?", "%#{sig}%").exists?
    end

    def format_bytes(n)
      return "#{n} B" if n < 1024
      kib = n / 1024.0
      return "#{kib.round(1)} KiB" if kib < 1024
      "#{(kib / 1024).round(2)} MiB"
    end
    helper_method :format_bytes

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
