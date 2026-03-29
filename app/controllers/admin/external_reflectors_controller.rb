module Admin
  class ExternalReflectorsController < ApplicationController
    layout false
    before_action :require_admin
    before_action :set_reflector, only: %i[edit update destroy]

    def index
      @reflectors = ExternalReflector.ordered
    end

    def new
      @reflector = ExternalReflector.new
    end

    def create
      @reflector = ExternalReflector.new(reflector_params)
      if @reflector.save
        redirect_to admin_external_reflectors_path, notice: "\"#{@reflector.name}\" added."
      else
        render :new, status: :unprocessable_entity
      end
    end

    def edit; end

    def update
      if @reflector.update(reflector_params)
        redirect_to admin_external_reflectors_path, notice: "\"#{@reflector.name}\" updated."
      else
        render :edit, status: :unprocessable_entity
      end
    end

    def destroy
      @reflector.destroy
      redirect_to admin_external_reflectors_path, notice: "\"#{@reflector.name}\" removed."
    end

    private

    def set_reflector
      @reflector = ExternalReflector.find(params[:id])
    end

    def reflector_params
      params.require(:external_reflector).permit(:name, :status_url, :portal_url, :description, :enabled, :poll)
    end
  end
end
