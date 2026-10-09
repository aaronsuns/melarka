-- Every job's thumbnail is YouTube's hqdefault (spec §15.2); fixes pasted-link jobs that had none.
UPDATE downloads SET thumbnail = 'https://i.ytimg.com/vi/' || video_id || '/hqdefault.jpg' WHERE video_id != '';
