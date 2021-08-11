package goinsta

import (
	"bytes"
	cryptRand "crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

type UploadOptions struct {
	insta *Instagram

	// File to upload, can be one of jpeg, jpg, mp4
	File io.Reader

	// Thumbnail to use for videos, one of jpeg or jpg. If not set a thumbnail
	//   will be extracted automatically
	Thumbnail io.Reader

	// Multiple images, to post a carousel or multiple stories at once
	Album []io.Reader

	// Caption text, or decription if IGTV
	Caption string

	// Set to true if you want to post a story
	IsStory bool

	// IGTV settings
	IsIGTV      bool
	Title       string
	IGTVPreview bool

	// Set to true if you want to mute the audio
	MuteAudio bool

	// Option flags, set to true disable
	DisableComments      bool
	DisableLikeViewCount bool
	DisableSubtitles     bool

	// Used to tag users in posts
	UserTags *[]UserTag

	// Used to provide a location for a post
	Location     *LocationTag
	locationJson string

	// File properties
	width     int
	height    int
	duration  int
	mediaType int

	// Internal config
	config         map[string]interface{}
	configURL      string
	uploadID       string
	streamID       string
	waterfallID    string
	ruploadParams  string
	name           string
	startTime      string
	videoGroupID   string // used for story multi-video upload
	index          int    // used for story multi-video upload
	offset         int
	segmentType    int
	isSidecar      bool
	useXSharingIDs bool
	isThumbnail    bool

	// File buf
	buf *bytes.Buffer

	// Formatted UserTags
	userTags *postTags
	tagsJson string
}

// UserTag represents a user post tag. Position is optional, a random
//   position will be used if not provided. For videos the position will always
//   be [0,0], and doesn't need to be provided.
type UserTag struct {
	User     *User      `json:"user_id"`
	Position [2]float64 `json:"position"`
}

type postTags struct {
	In []postTagUser `json:"in"`
}

type postTagUser struct {
	UserID   int64      `json:"user_id"`
	Position [2]float64 `json:"position"`
}

// LocationTag represents a post location tag
type LocationTag struct {
	Name           string  `json:"name"`
	Address        string  `json:"address"`
	Lat            float64 `json:"lat"`
	Lng            float64 `json:"lng"`
	ExternalSource string  `json:"external_source"`
	PlacesID       string  `json:"facebook_places_id"`
}

func formatUserTags(tags []UserTag, isVideo bool) *postTags {
	var f []postTagUser
	for _, tag := range tags {
		u := postTagUser{}
		if tag.User != nil {
			u = newPostTagUser(tag, isVideo)
		}
		f = append(f, u)
	}
	return &postTags{In: f}
}

func newPostTagUser(tag UserTag, isVideo bool) postTagUser {
	p1, p2 := 0.0, 0.0
	if !isVideo {
		// Extensive calculation to make sure its a float with 9 decimals
		r1 := float64(random(1000000, 9999999)) / 10000000.0
		r2 := float64(random(1000000, 9999999)) / 10000000.0

		p1 := rand.Float64() + r1
		p2 := rand.Float64() + r2
		if tag.Position != [2]float64{0, 0} {
			p1 = tag.Position[0] + r1
			p2 = tag.Position[1] + r2
		}

		// Make sure its not more than 1, or less than 0, with some random margin
		p1 = math.Min(p1, 0.95-r1)
		p2 = math.Min(p2, 0.95-r2)
		p1 = math.Max(p1, 0.012+r1)
		p2 = math.Max(p2, 0.012+r2)
	}
	return postTagUser{
		UserID:   tag.User.ID,
		Position: [2]float64{p1, p2},
	}
}

// NewPostTag creates a LocationTag from a location, which can be used as a
//   location tag in posts.
func (l *Location) NewPostTag() *LocationTag {
	return &LocationTag{
		Name:           l.Name,
		Address:        l.Address,
		Lat:            l.Lat,
		Lng:            l.Lng,
		ExternalSource: l.ExternalSource,
		PlacesID:       toString(l.FacebookPlacesID),
	}
}

func (o *UploadOptions) uploadPhoto() error {
	// Set media type to photo
	o.mediaType = 1

	// Create unique upload id and name
	if o.uploadID == "" {
		o.newUploadID()
	}
	if o.name == "" {
		rand := random(1000000000, 9999999999)
		o.name = o.uploadID + "_0_" + toString(rand)

	}
	if o.waterfallID == "" {
		o.waterfallID = generateUUID()
	}

	// Get image properties
	width, height, err := getImageSize(o.File)
	if err != nil {
		return err
	}
	o.width, o.height = width, height

	// Create Rupload header params
	o.createRUploadParams()

	err = o.postPhoto()
	if err != nil {
		return err
	}

	o.createPhotoConfig()
	return nil
}

func (o *UploadOptions) configurePost(video bool) (*Item, error) {
	insta := o.insta

	query := MergeMapI(
		o.config,
		map[string]interface{}{
			"camera_entry_point":         "35",
			"_uid":                       toString(insta.Account.ID),
			"_uuid":                      insta.uuid,
			"device_id":                  insta.dID,
			"creation_logger_session_id": generateUUID(),
			"nav_chain":                  "",
			"multi_sharing":              "1",
		},
	)

	if o.locationJson != "" {
		query["location"] = o.locationJson
	}
	o.config = query
	o.configURL = urlConfigure
	if video {
		o.configURL += "?video=1"
	}

	return o.configure()
}

func (o *UploadOptions) configureVideo() (*Item, error) {
	return o.configurePost(true)
}

func (o *UploadOptions) configureImage() (*Item, error) {
	return o.configurePost(false)
}

func (o *UploadOptions) configureIGTV() (*Item, error) {
	insta := o.insta

	query := MergeMapI(
		o.config,
		map[string]interface{}{
			"_uid":                       toString(insta.Account.ID),
			"_uuid":                      insta.uuid,
			"device_id":                  insta.dID,
			"creation_logger_session_id": generateUUID(),
			"nav_chain":                  "",
			"multi_sharing":              "1",
		},
	)

	o.config = query
	o.configURL = urlConfigureIGTV

	return o.configure()
}

func (o *UploadOptions) configureStory(video bool) (*Item, error) {
	insta := o.insta

	query := MergeMapI(
		o.config,
		map[string]interface{}{
			"_uid":      toString(insta.Account.ID),
			"_uuid":     insta.uuid,
			"device_id": insta.dID,
			"nav_chain": "",
		},
	)

	o.config = query
	o.configURL = urlConfigureStory
	if video {
		o.configURL += "?video=1"
	}

	return o.configure()
}

func (o *UploadOptions) postThumbnail() error {
	buf, err := readFile(o.Thumbnail)
	if err != nil {
		return err
	}
	o.buf = buf

	rand := random(1000000000, 9999999999)
	o.name = o.uploadID + "_0_" + toString(rand)
	o.waterfallID = generateUUID()

	// Create Rupload header params
	o.createRUploadParams()

	err = o.postPhoto()
	if err != nil {
		return err
	}

	return nil
}

func (o *UploadOptions) postVideo() error {
	insta := o.insta

	// Upload video bytes
	body, _, err := insta.sendRequest(
		&reqOptions{
			Endpoint:  fmt.Sprintf(urlUploadVideo, o.name),
			IsPost:    true,
			DataBytes: o.buf,
			ExtraHeaders: map[string]string{
				"X-Entity-Name":              o.name,
				"X-Entity-Type":              http.DetectContentType(o.buf.Bytes()),
				"X-Entity-Length":            toString(o.buf.Len()),
				"X-Instagram-Rupload-Params": o.ruploadParams,
				"Offset":                     "0",
				"Content-type":               "application/octet-stream",
				"X_fb_photo_waterfall_id":    o.waterfallID,
			},
		},
	)
	if err != nil {
		return err
	}

	var result struct {
		UploadID       string      `json:"upload_id"`
		XsharingNonces interface{} `json:"xsharing_nonces"`
		Status         string      `json:"status"`
	}
	err = json.Unmarshal(body, &result)
	if err != nil {
		return err
	}
	if result.Status != "ok" {
		return fmt.Errorf("unknown error, status: %s", result.Status)
	}

	return nil
}

// postVideoGET - every video upload is a sequence of a get request, followed
//   by a post request to upload the bytes.
func (o *UploadOptions) postVideoGET() error {
	insta := o.insta

	headers := map[string]string{
		"X-Instagram-Rupload-Params": o.ruploadParams,
		"X_fb_video_waterfall_id":    o.waterfallID,
		"Offset":                     toString(o.offset),
	}
	if o.streamID != "" {
		headers["Stream-Id"] = o.streamID
		// Segment type = 1 for last segment, 2 for all others
		headers["Segment-Type"] = toString(o.segmentType)
	}
	_, _, err := insta.sendRequest(
		&reqOptions{
			Endpoint:     fmt.Sprintf(urlUploadVideo, o.name),
			ExtraHeaders: headers,
		},
	)
	return err
}

func (o *UploadOptions) postPhoto() error {
	insta := o.insta

	// Upload Photo
	body, _, err := insta.sendRequest(
		&reqOptions{
			Endpoint:  fmt.Sprintf(urlUploadPhoto, o.name),
			IsPost:    true,
			DataBytes: o.buf,
			ExtraHeaders: map[string]string{
				"X-Entity-Name":              o.name,
				"X-Entity-Type":              http.DetectContentType(o.buf.Bytes()),
				"X-Entity-Length":            toString(o.buf.Len()),
				"X-Instagram-Rupload-Params": o.ruploadParams,
				"Offset":                     "0",
				"Content-type":               "application/octet-stream",
				"X_fb_photo_waterfall_id":    o.waterfallID,
			},
		},
	)

	// Parse Result
	var result struct {
		UploadID       string      `json:"upload_id"`
		XsharingNonces interface{} `json:"xsharing_nonces"`
		Status         string      `json:"status"`
	}
	err = json.Unmarshal(body, &result)
	if err != nil {
		return err
	}
	if result.Status != "ok" {
		return fmt.Errorf("unknown error, status: %s", result.Status)
	}
	return nil
}

func (o *UploadOptions) createRUploadParams(extra ...map[string]string) error {
	// Default common parameters
	params := map[string]string{
		"retry_context":     `{"num_step_auto_retry": 0, "num_reupload": 0, "num_step_manual_retry": 0}`,
		"media_type":        toString(o.mediaType),
		"upload_id":         o.uploadID,
		"xsharing_user_ids": "[]",
	}

	// Add specific params per type
	switch o.mediaType {
	case 1:
		params = MergeMapS(params, map[string]string{
			"image_compression": `{"lib_name": "moz", "lib_version": "3.1.m", "quality": "80"}`,
		})
	case 2:
		if o.isThumbnail {
			break
		}
		params = MergeMapS(params, map[string]string{
			"video_format":             "video/mp4",
			"upload_media_height":      toString(o.height),
			"upload_media_width":       toString(o.width),
			"upload_media_duration_ms": toString(o.duration),
		})
		if o.IsIGTV {
			params["is_igtv_video"] = "1"
		}
		if o.Thumbnail == nil {
			params["content_tags"] = "use_default_cover"
			// params["extract_cover_frame"] = "1"  // test this out
		}
	}
	if o.isSidecar {
		params["is_sidecar"] = "1"
	}
	if o.useXSharingIDs {
		var ids []string
		for _, tag := range *o.UserTags {
			ids = append(ids, toString(tag.User.ID))
		}
		b, err := json.Marshal(ids)
		if err != nil {
			return err
		}
		params["xsharing_user_ids"] = string(b)
	}
	if o.IsStory && o.mediaType == 2 {
		params["extract_cover_frame"] = "1"
		params["content_tags"] = "use_default_cover"
		params["for_direct_story"] = "1"
		params["for_album"] = "1"
	}

	// Add extra params
	for _, e := range extra {
		for k, v := range e {
			params[k] = v
		}
	}

	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	o.ruploadParams = string(b)
	return nil
}

func (o *UploadOptions) createPhotoConfig() {
	config := map[string]interface{}{
		"scene_capture_type": "",
		"upload_id":          o.uploadID,
		"timezone_offset":    timeOffset,
		"source_type":        "4", // 3 = camera, 4 = library
		"scene_type":         nil,
		"device": map[string]interface{}{
			"manufacturer":    deviceSettings["manufacturer"],
			"model":           deviceSettings["model"],
			"android_version": deviceSettings["android_version"],
			"android_release": deviceSettings["android_release"],
		},
		"edits": map[string]interface{}{
			"crop_original_size": []int{o.width * 1.0, o.height * 1.0},
			"crop_center":        []float32{0.0, 0.0},
			"crop_zoom":          1.0,
		},
		"extra": map[string]interface{}{
			"source_width":  o.width,
			"source_height": o.height,
		},
	}

	if o.tagsJson != "" {
		config["usertags"] = o.tagsJson
	}
	if o.IsStory {
		// TODO
	}
	o.config = config
}

func (o *UploadOptions) createVideoConfig() error {
	// Duration in seconds
	length := fmt.Sprintf("%.3f", float64(o.duration)/1000)

	config := map[string]interface{}{
		"filter_type":  "0",
		"caption":      o.Caption,
		"upload_id":    o.uploadID,
		"source_type":  "4",
		"video_result": "",
		// this is only set to the current time for source type 3
		"date_time_original":      "19040101T000000.000Z",
		"timezone_offset":         timeOffset,
		"video_subtitles_enabled": "1",
		"multi_sharing":           "1",
		"device": map[string]interface{}{
			"manufacturer":    deviceSettings["manufacturer"],
			"model":           deviceSettings["model"],
			"android_version": deviceSettings["android_version"],
			"android_release": deviceSettings["android_release"],
		},
		"length": length,
		"clips": map[string]interface{}{
			"length":      length,
			"source_type": "4",
		},
		"extra": map[string]interface{}{
			"source_width":  o.width,
			"source_height": o.height,
		},
		"audio_muted":        o.MuteAudio,
		"poster_frame_index": 0, // TODO: look into this (testing to see if it matters which index is used)
	}

	if o.UserTags != nil && !(o.IsIGTV || o.IsStory) {
		tags := formatUserTags(*o.UserTags, true)
		b, err := json.Marshal(tags)
		if err != nil {
			return err
		}
		config["usertags"] = string(b)
	}
	if o.DisableLikeViewCount && !(o.IsIGTV || o.IsStory) {
		config["like_and_view_counts_disabled"] = "1"
	}
	if o.DisableSubtitles && !o.IsStory {
		config["video_subtitles_enabled"] = "0"
	}
	if !o.isSidecar {
		config["camera_entry_point"] = "34"
	}
	if o.IsIGTV {
		config["camera_entry_point"] = "171"
		config["title"] = o.Title
		config["igtv_ads_toggled_on"] = "0"
		config["keep_shoppable_products"] = "0"
		config["igtv_composer_session_id"] = generateUUID()
		config["igtv_share_preview_to_feed"] = "0"
		if o.IGTVPreview {
			config["igtv_share_preview_to_feed"] = "1"
		}
	}
	if o.IsStory {
		supCap, err := getSupCap()
		if err != nil {
			return err
		}

		config["camera_entry_point"] = "1"
		config["media_folder"] = "Instagram"
		config["supported_capabilities_new"] = supCap
		config["original_media_type"] = "video"
		config["configure_mode"] = "1"
		config["implicit_location"] = map[string]interface{}{
			"media_location": map[string]interface{}{
				"lat": 0.0,
				"lng": 0.0,
			},
		}
		config["client_timestamp"] = toString(time.Now().Unix())
		config["client_shared_at"] = o.startTime
		config["segmented_video_group_id"] = o.videoGroupID
		config["composition_id"] = generateUUID()

		if o.index == 0 {
			config["capture_type"] = "normal"
		}

		if len(o.Album) > 1 {
			config["allow_multi_configures"] = "1"
			config["is_segmented_video"] = "1"
			config["is_multi_upload"] = "1"
			config["poster_frame_index"] = -100 * o.index
		}
	}
	o.config = config
	return nil
}

func (o *UploadOptions) uploadAlbum() (*Item, error) {
	insta := o.insta
	o.isSidecar = true
	o.waterfallID = generateUUID()

	if len(o.Album) > 10 {
		return nil, ErrCarouselMediaLimit
	}

	// Upload photos one by one
	var metadata []map[string]interface{}
	for _, media := range o.Album {

		// Read media into memory
		buf, err := readFile(media)
		if err != nil {
			return nil, err
		}

		// Validate file type
		o.buf = buf
		t := http.DetectContentType(buf.Bytes())
		if !(t == "image/jpeg" || t == "image/mp4") {
			return nil, ErrInvalidFormat
		}

		// Upload Media
		if t == "image/jpeg" {
			// Create upload id & name
			o.newUploadID()
			rand := random(1000000000, 9999999999)
			o.name = o.uploadID + "_0_" + toString(rand)

			err := o.uploadPhoto()
			if err != nil {
				return nil, err
			}
		} else if t == "image/mp4" {
			err := o.uploadVideo()
			if err != nil {
				return nil, err
			}
		}

		metadata = append(metadata, o.config)
	}

	// Album upload id
	o.newUploadID()

	// Create request payload
	query := map[string]interface{}{
		"camera_entry_point": "35",
		"timezone_offset":    timeOffset,
		"source_type":        "4",
		"_uid":               toString(insta.Account.ID),
		"device_id":          insta.dID,
		"_uuid":              insta.uuid,
		"nav_chain":          "",
		"caption":            o.Caption,
		"client_sidecar_id":  o.uploadID,
		"upload_id":          o.uploadID,
		"children_metadata":  metadata,
	}

	if o.locationJson != "" {
		query["location"] = o.locationJson
	}
	o.config = query
	o.configURL = urlConfigureSidecar

	// Configure carousel media
	return o.configure()
}

func (o *UploadOptions) configure() (*Item, error) {
	insta := o.insta

	// Create request query
	data, err := json.Marshal(o.config)
	if err != nil {
		return nil, err
	}

	body, _, err := insta.sendRequest(&reqOptions{
		Endpoint: o.configURL,
		IsPost:   true,
		Query:    generateSignature(data),
	})
	if err != nil {
		return nil, err
	}

	var res struct {
		Media   Item   `json:"media"`
		Message string `json:"message"`
		Status  string `json:"status"`
	}
	err = json.Unmarshal(body, &res)
	if err != nil {
		return nil, err
	}

	if res.Status != "ok" && res.Message == "Transcode not finished yet." {
		insta.ErrHandler(fmt.Errorf("%s, %s. Please wait.", res.Status, res.Message))
		time.Sleep(6 * time.Second)
		return o.configure()
	} else if res.Status != "ok" {
		return nil, fmt.Errorf("invalid status, result: %s, %s", res.Status, res.Message)
	}
	return &res.Media, nil
}

func (insta *Instagram) Upload(o *UploadOptions) (*Item, error) {
	o.insta = insta
	o.startTime = toString(time.Now().Unix())

	// Format User & Location Tags
	err := o.processTags()
	if err != nil {
		return nil, err
	}

	// Multiple file uploads
	if len(o.Album) > 0 && !o.IsStory {
		// Upload carousel
		return o.uploadAlbum()
	} else if len(o.Album) > 0 && o.IsStory {
		// Upload multiple story videos
		return o.uploadMultiStory()
	}

	// Single file uploads
	// Read file into memory
	buf, err := readFile(o.File)
	if err != nil {
		return nil, err
	}
	o.buf = buf

	// Check file type
	t := http.DetectContentType(buf.Bytes())
	if t == "image/jpeg" {
		err := o.uploadPhoto()
		if err != nil {
			return nil, err
		}
		return o.configureImage()
	} else if t == "image/mp4" {
		err := o.uploadVideo()
		if err != nil {
			return nil, err
		}
		return o.configureVideo()
	}

	return nil, ErrInvalidFormat
}

func (o *UploadOptions) uploadMultiStory() (*Item, error) {
	o.videoGroupID = generateUUID()
	o.segmentType = 3
	o.mediaType = 2

	// Validate videos
	for _, vid := range o.Album {
		buf, err := readFile(vid)
		if err != nil {
			return nil, err
		}
		t := http.DetectContentType(buf.Bytes())
		if t != "image/mp4" {
			return nil, ErrStoryBadMediaType
		}

		// Get video info
		width, height, duration, err := getVideoInfo(buf.Bytes())
		if err != nil {
			return nil, err
		}
		o.width, o.height, o.duration = width, height, duration

		if duration > 15000 {
			return nil, ErrStoryMediaTooLong
		}
	}

	s := make([]byte, 6)
	cryptRand.Read(s)
	suffix := fmt.Sprintf("_%X_Mixed_0", s)

	// Upload Media
	var item *Item
	for i, vid := range o.Album {
		o.index = i
		buf, err := readFile(vid)
		if err != nil {
			return nil, err
		}
		o.buf = buf

		width, height, duration, err := getVideoInfo(o.buf.Bytes())
		if err != nil {
			return nil, err
		}
		o.width, o.height, o.duration = width, height, duration

		o.newUploadID()
		o.waterfallID = o.uploadID + suffix
		o.newSegmentName(buf.Len())

		err = o.segmentTransfer(buf.Bytes())
		if err != nil {
			return nil, err
		}

		err = o.createVideoConfig()
		if err != nil {
			return nil, err
		}

		it, err := o.configureStory(true)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			item = it
		}
	}

	// only returns the first story item
	return item, nil
}

func (o *UploadOptions) uploadVideo() error {
	// Set media type to video
	o.mediaType = 2
	o.useXSharingIDs = true
	o.newUploadID()

	// Get video info
	width, height, duration, err := getVideoInfo(o.buf.Bytes())
	if err != nil {
		return err
	}
	o.width, o.height, o.duration = width, height, duration

	// Create Rupload header params
	err = o.createRUploadParams()
	if err != nil {
		return err
	}

	// If video size greater than twice the threshold, use segments
	t := 1 << 22
	if o.buf.Len() > 2*t {
		err := o.segmentVideo(t)
		if err != nil {
			return err
		}
	} else {
		// If not segmented, upload video directly
		// Create unique upload id and name
		rand := random(1000000000, 9999999999)
		o.name = fmt.Sprintf("%s_0_%d", o.uploadID, rand)
		o.waterfallID = generateUUID()

		// Initialize the upload with a get request
		err = o.postVideoGET()
		if err != nil {
			return err
		}

		err = o.postVideo()
		if err != nil {
			return err
		}
	}

	if o.Thumbnail != nil {
		err := o.postThumbnail()
		if err != nil {
			return err
		}
	}

	err = o.createVideoConfig()
	return err
}

func (o *UploadOptions) segmentVideo(t int) error {
	o.waterfallID = toString(time.Now().Unix())

	err := o.segmentPhase("start")
	if err != nil {
		return err
	}

	segments := o.createSegments(t)
	length := len(*segments)
	o.segmentType = 2
	for i, segment := range *segments {
		if i == length-1 {
			o.segmentType = 1
		}

		// Create new name for each request
		o.newSegmentName(len(segment))

		err = o.postVideoGET()
		if err != nil {
			return err
		}

		err = o.segmentTransfer(segment)
		if err != nil {
			return err
		}
		o.offset += len(segment)

	}

	err = o.segmentPhase("end")
	return err
}

func (o *UploadOptions) newSegmentName(l int) {
	id := strings.ReplaceAll(generateUUID(), "-", "")
	t := time.Now().Unix()
	t = t - (t % 1000)
	o.name = fmt.Sprintf("%s-0-%d-%d-%d", id, l, t, t)
}

func (o *UploadOptions) segmentTransfer(segment []byte) error {
	insta := o.insta

	headers := map[string]string{
		"X-Entity-Name":              o.name,
		"X-Entity-Type":              http.DetectContentType(o.buf.Bytes()),
		"X-Entity-Length":            toString(len(segment)),
		"X-Instagram-Rupload-Params": o.ruploadParams,
		"Offset":                     "0",
		"Segment-Start-Offset":       toString(o.offset),
		"Content-type":               "application/octet-stream",
		"Segment-Type":               toString(o.segmentType),
		"X_fb_photo_waterfall_id":    o.waterfallID,
	}
	if o.streamID != "" {
		headers["Stream-Id"] = o.streamID
	}

	// Upload video bytes
	body, _, err := insta.sendRequest(
		&reqOptions{
			Endpoint:     fmt.Sprintf(urlUploadVideo, o.name),
			IsPost:       true,
			DataBytes:    bytes.NewBuffer(segment),
			ExtraHeaders: headers,
		},
	)
	if err != nil {
		return err
	}

	var result struct {
		UploadID       string      `json:"upload_id"`
		XsharingNonces interface{} `json:"xsharing_nonces"`
		Status         string      `json:"status"`
	}
	err = json.Unmarshal(body, &result)
	if err != nil {
		return err
	}
	if result.Status != "ok" {
		return fmt.Errorf("unknown error, status: %s", result.Status)
	}

	return nil
}

func (o *UploadOptions) segmentPhase(phase string) error {
	insta := o.insta

	body, _, err := insta.sendRequest(
		&reqOptions{
			Endpoint: fmt.Sprintf("%s%s?segmented=true&phase=%s", urlUploadVideo, generateUUID(), phase),
			IsPost:   true,
			ExtraHeaders: map[string]string{
				"X-Instagram-Rupload-Params": o.ruploadParams,
			},
		},
	)
	if err != nil {
		return err
	}
	var res struct {
		StreamID int64  `json:"stream_id"`
		Status   string `json:"status"`
	}
	err = json.Unmarshal(body, &res)
	if err != nil {
		return err
	}
	if res.Status != "ok" {
		return fmt.Errorf("invalid status, result: %s", res.Status)
	}
	if phase == "start" {
		o.streamID = toString(res.StreamID)
	}
	return nil
}

func (o *UploadOptions) createSegments(t int) *[][]byte {
	var segments [][]byte
	for o.buf.Len() != 0 {
		// For some reason the byte lengths the insta app uploads are never
		//   the same, so adding a random difference to the segment sizes.
		r := random(0, 10000)
		if rand.Float64() < 0.65 {
			segments = append(segments, o.buf.Next(t-r))
		} else {
			segments = append(segments, o.buf.Next(t+r))
		}
	}
	return &segments
}

func readFile(f io.Reader) (*bytes.Buffer, error) {
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(f)
	return buf, err
}

func (o *UploadOptions) processTags() error {
	if o.UserTags != nil {
		o.userTags = formatUserTags(*o.UserTags, false)
		b, err := json.Marshal(o.userTags)
		if err != nil {
			return err
		}
		o.tagsJson = string(b)
	}

	if o.Location != nil {
		b, err := json.Marshal(o.Location)
		if err != nil {
			return err
		}
		o.locationJson = string(b)
	}
	return nil
}

func (o *UploadOptions) newUploadID() {
	o.uploadID = toString(random(1000000000, 9999999999))
}