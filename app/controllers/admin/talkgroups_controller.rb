module Admin
  class TalkgroupsController < ApplicationController
    layout false
    before_action :require_admin
    before_action :set_talkgroup, only: %i[edit update destroy]

    def index
      @talkgroups = Talkgroup.order(:number)
    end

    def new
      @talkgroup = Talkgroup.new
    end

    def create
      @talkgroup = Talkgroup.new(talkgroup_params)
      if @talkgroup.save
        redirect_to admin_talkgroups_path, notice: "Talkgroup #{@talkgroup.number} created"
      else
        render :new, status: :unprocessable_entity
      end
    end

    def edit
    end

    def update
      if @talkgroup.update(talkgroup_params)
        redirect_to admin_talkgroups_path, notice: "Talkgroup #{@talkgroup.number} updated"
      else
        render :edit, status: :unprocessable_entity
      end
    end

    def destroy
      @talkgroup.destroy
      redirect_to admin_talkgroups_path, notice: "Talkgroup #{@talkgroup.number} deleted"
    end

    private

    def set_talkgroup
      @talkgroup = Talkgroup.find(params[:id])
    end

    def talkgroup_params
      params.require(:talkgroup).permit(:number, :name)
    end
  end
end
