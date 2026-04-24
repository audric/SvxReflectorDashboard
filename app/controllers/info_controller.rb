class InfoController < ApplicationController
  layout false
  skip_before_action :require_login, raise: false

  def show
    @body = Setting.get("system_description", "")
  end
end
