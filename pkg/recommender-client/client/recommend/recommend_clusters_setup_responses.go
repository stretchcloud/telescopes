// Code generated by go-swagger; DO NOT EDIT.

package recommend

// This file was generated by the swagger tool.
// Editing this file might prove futile when you re-run the swagger generate command

import (
	"fmt"
	"io"

	"github.com/go-openapi/runtime"

	strfmt "github.com/go-openapi/strfmt"

	models "github.com/banzaicloud/telescopes/pkg/recommender-client/models"
)

// RecommendClustersSetupReader is a Reader for the RecommendClustersSetup structure.
type RecommendClustersSetupReader struct {
	formats strfmt.Registry
}

// ReadResponse reads a server response into the received o.
func (o *RecommendClustersSetupReader) ReadResponse(response runtime.ClientResponse, consumer runtime.Consumer) (interface{}, error) {
	switch response.Code() {

	case 200:
		result := NewRecommendClustersSetupOK()
		if err := result.readResponse(response, consumer, o.formats); err != nil {
			return nil, err
		}
		return result, nil

	default:
		return nil, runtime.NewAPIError("unknown error", response, response.Code())
	}
}

// NewRecommendClustersSetupOK creates a RecommendClustersSetupOK with default headers values
func NewRecommendClustersSetupOK() *RecommendClustersSetupOK {
	return &RecommendClustersSetupOK{}
}

/*RecommendClustersSetupOK handles this case with default header values.

RecommendationResponse
*/
type RecommendClustersSetupOK struct {
	Payload *models.ClusterRecommendationResp
}

func (o *RecommendClustersSetupOK) Error() string {
	return fmt.Sprintf("[POST /recommender/][%d] recommendClustersSetupOK  %+v", 200, o.Payload)
}

func (o *RecommendClustersSetupOK) readResponse(response runtime.ClientResponse, consumer runtime.Consumer, formats strfmt.Registry) error {

	o.Payload = new(models.ClusterRecommendationResp)

	// response payload
	if err := consumer.Consume(response.Body(), o.Payload); err != nil && err != io.EOF {
		return err
	}

	return nil
}
