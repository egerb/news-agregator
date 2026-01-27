# Small app to fetch news from network
## Key features
### LLM config
### Google Sheets Configuration
### Scheduler Configuration
### Topics/Prompts Pairs

<img width="1249" height="671" alt="image" src="https://github.com/user-attachments/assets/5ab31bff-3ffa-4641-aef7-8d2f19b1aa03" />
<img width="1249" height="671" alt="image" src="https://github.com/user-attachments/assets/3557058b-4727-4779-bd2b-849ebfdcd6dd" />
<img width="1249" height="671" alt="image" src="https://github.com/user-attachments/assets/c16439d4-0b2f-4a1e-9bbb-5a20c0b99a2a" />
<img width="1249" height="671" alt="image" src="https://github.com/user-attachments/assets/a80264a5-814c-4b0d-adb2-1273a5438d27" />



# Google Sheets API Credentials Setup Guide

## Overview
To manage Google Sheets programmatically, you need to set up a Google Cloud Project and obtain service account credentials.

## Step-by-Step Instructions

### Step 1: Create a Google Cloud Project

1. Go to [Google Cloud Console](https://console.cloud.google.com/)
2. Click on the project dropdown at the top
3. Click **"New Project"**
4. Enter a project name (e.g., "News Aggregator")
5. Click **"Create"**
6. Wait for the project to be created and select it

### Step 2: Enable Google Sheets API

1. In the Google Cloud Console, go to **"APIs & Services"** > **"Library"**
2. Search for **"Google Sheets API"**
3. Click on it and press **"Enable"**
4. Also enable **"Google Drive API"** (required for accessing spreadsheets)
   - Search for "Google Drive API"
   - Click and enable it

### Step 3: Create a Service Account

1. Go to **"APIs & Services"** > **"Credentials"**
2. Click **"Create Credentials"** > **"Service Account"**
3. Fill in the details:
   - **Service account name**: `news-aggregator-service` (or any name)
   - **Service account ID**: Auto-generated (you can change it)
   - **Description**: "Service account for News Aggregator app"
4. Click **"Create and Continue"**
5. Skip the optional steps (Grant access, Grant users access) and click **"Done"**

### Step 4: Create and Download Service Account Key

1. In the **"Credentials"** page, find your newly created service account
2. Click on the service account email (e.g., `news-aggregator-service@your-project.iam.gserviceaccount.com`)
3. Go to the **"Keys"** tab
4. Click **"Add Key"** > **"Create new key"**
5. Select **"JSON"** format
6. Click **"Create"**
7. A JSON file will be downloaded automatically - **SAVE THIS FILE SECURELY**
   - This file contains your private key - never commit it to version control!

### Step 5: Share Google Sheet with Service Account

1. Open your Google Sheet (or create a new one)
2. Click the **"Share"** button (top right)
3. Copy the **Service Account Email** from the JSON file you downloaded:
   - Open the JSON file
   - Find the `client_email` field (e.g., `news-aggregator-service@your-project.iam.gserviceaccount.com`)
4. Paste this email into the "Share" dialog
5. Give it **"Editor"** permissions (or at least "Editor" to write data)
6. Click **"Send"** (you can uncheck "Notify people" if you want)

### Step 6: Get Spreadsheet ID

1. Open your Google Sheet
2. Look at the URL in your browser:
   ```
   https://docs.google.com/spreadsheets/d/SPREADSHEET_ID/edit
   ```
3. Copy the `SPREADSHEET_ID` (the long string between `/d/` and `/edit`)
4. You'll need this ID in your application configuration

## Credentials File Structure

The downloaded JSON file will look like this:

```json
{
  "type": "service_account",
  "project_id": "your-project-id",
  "private_key_id": "key-id",
  "private_key": "-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----\n",
  "client_email": "service-account@your-project.iam.gserviceaccount.com",
  "client_id": "123456789",
  "auth_uri": "https://accounts.google.com/o/oauth2/auth",
  "token_uri": "https://oauth2.googleapis.com/token",
  "auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
  "client_x509_cert_url": "https://www.googleapis.com/robot/v1/metadata/x509/..."
}
```

## What You Need for the Application

You'll need to provide these in the web interface:

1. **Credentials JSON File**: Upload the entire JSON file (or paste its contents)
2. **Spreadsheet ID**: The ID from the Google Sheet URL
3. **Sheet Name**: The name of the sheet tab (default: "news")


