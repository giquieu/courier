1.4.1-courier-7.1.0
----------  
  * Fix receiving attachments and quick replies

1.4.0-courier-7.1.0
----------  
  * Integration support with Microsoft Teams

1.3.3-courier-7.1.0
----------  
  * Media message template support, link preview and document name correction on WhatsApp Cloud #118

1.3.2-courier-7.1.0
----------
  * Fix to prevent create a new contact without extra 9 in wpp number, instead, updating if already has one with the extra 9, handled in whatsapp cloud channels #119

1.3.1-courier-7.1.0
----------
  * Fix to ensure update last_seen_on if there is no error and no failure to send the message.

1.3.0-courier-7.1.0
----------
  * Slack Bot Channel Handler
  * Whatsapp Cloud Handler

1.2.1-courier-7.1.0
----------
  * Update contact last_seen_on on send message to him

1.2.0-courier-7.1.0
----------
  * Merge tag v7.1.0 from nyaruka into our 1.1.8-courier-7.0.0

1.1.8-courier-7.0.0
----------
 * Fix whatsapp handler to update the contact URN if the wa_id returned in the send message request is different from the current URN path, avoiding creating a new contact.

1.1.7-courier-7.0.0
----------
 * Add library with greater support for detection of mime types in Whatsapp

1.1.6-courier-7.0.0
----------
 * Support for viewing sent links in Whatsapp messages

1.1.5-courier-7.0.0
----------
 * Fix sending document names in whatsapp media message templates

1.1.4-courier-7.0.0
----------
 * Add Kyrgyzstan language support in whatsapp templates

1.1.3-courier-7.0.0
----------
 * fix whatsapp uploaded attachment file name

1.1.2-courier-7.0.0
----------
 * Fix metadata fetching for new Facebook contacts

1.1.1-courier-7.0.0
----------
 * Add Instagram Handler
 * Update gocommon to v1.16.2

1.1.0-courier-7.0.0
----------
 * Fix: Gujarati whatsapp language code
 * add button layout support on viber channel

1.0.0-courier-7.0.0
----------
 * Update Dockerfile to go 1.17.5 
 * Support to facebook customer feedback template
 * Support whatsapp media message template
 * Fix to prevent requests from blocked contact generate channel log
 * Weni-Webchat handler
 * Support to build Docker image
