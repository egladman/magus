package eval

import ()

func (s *evalSuite) TestFailureReasonBucketsByCode() {
	s.Equal("BZZ1005", failureReason("[BZZ1005] buzz: x.buzz:1:1: cannot assign"))
	s.Equal("other", failureReason("buzz: x.buzz:3:1: if statement is not allowed"))
}
