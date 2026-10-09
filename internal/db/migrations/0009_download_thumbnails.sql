-- Every job's thumbnail is YouTube's hqdefault; fixes pasted-link jobs that had none.
UPDATE downloads SET thumbnail = 'https://i.ytimg.com/vi/' || video_id || '/hqdefault.jpg' WHERE video_id != '';
