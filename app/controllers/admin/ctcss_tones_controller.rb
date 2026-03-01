module Admin
  class CtcssTonesController < ApplicationController
    layout false
    before_action :require_admin
    before_action :set_ctcss_tone, only: %i[edit update destroy]

    def index
      @ctcss_tones = CtcssTone.order(:frequency)
    end

    def new
      @ctcss_tone = CtcssTone.new
    end

    def create
      @ctcss_tone = CtcssTone.new(ctcss_tone_params)
      if @ctcss_tone.save
        redirect_to admin_ctcss_tones_path, notice: "CTCSS tone #{@ctcss_tone.frequency} Hz created"
      else
        render :new, status: :unprocessable_entity
      end
    end

    def edit
    end

    def update
      if @ctcss_tone.update(ctcss_tone_params)
        redirect_to admin_ctcss_tones_path, notice: "CTCSS tone #{@ctcss_tone.frequency} Hz updated"
      else
        render :edit, status: :unprocessable_entity
      end
    end

    def destroy
      @ctcss_tone.destroy
      redirect_to admin_ctcss_tones_path, notice: "CTCSS tone #{@ctcss_tone.frequency} Hz deleted"
    end

    private

    def set_ctcss_tone
      @ctcss_tone = CtcssTone.find(params[:id])
    end

    def ctcss_tone_params
      params.require(:ctcss_tone).permit(:frequency, :code)
    end
  end
end
