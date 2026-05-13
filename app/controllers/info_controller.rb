class InfoController < ApplicationController
  layout false
  skip_before_action :require_login, raise: false

  def show
    @pages = InfoPage.published.ordered
    @page  = if params[:slug].present?
               @pages.find_by(slug: params[:slug])
             else
               @pages.first
             end
    return render plain: "Not found", status: :not_found if params[:slug].present? && @page.nil?

    @body = @page&.body.to_s
  end
end
