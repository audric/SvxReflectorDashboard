module Admin
  class TgsController < ApplicationController
    layout false
    before_action :require_admin
    before_action :set_tg, only: %i[edit update destroy]

    def index
      @tgs = Tg.ordered
    end

    def new
      @tg = Tg.new
    end

    def create
      @tg = Tg.new(tg_params)
      if @tg.save
        redirect_to admin_tgs_path, notice: "TG #{@tg.tg} created"
      else
        render :new, status: :unprocessable_entity
      end
    end

    def edit
    end

    def update
      if @tg.update(tg_params)
        redirect_to admin_tgs_path, notice: "TG #{@tg.tg} updated"
      else
        render :edit, status: :unprocessable_entity
      end
    end

    def destroy
      @tg.destroy
      redirect_to admin_tgs_path, notice: "TG #{@tg.tg} deleted"
    end

    private

    def set_tg
      @tg = Tg.find(params[:id])
    end

    def tg_params
      params.require(:tg).permit(:tg, :name, :description)
    end
  end
end
