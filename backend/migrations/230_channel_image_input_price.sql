-- Add a distinct image-input token price to both user billing and
-- account-statistics provider-cost pricing.
ALTER TABLE channel_model_pricing
    ADD COLUMN IF NOT EXISTS image_input_price NUMERIC(20,12);

ALTER TABLE channel_account_stats_model_pricing
    ADD COLUMN IF NOT EXISTS image_input_price NUMERIC(20,12);
